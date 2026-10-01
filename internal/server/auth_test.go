package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ssh-bridge/internal/config"
	"ssh-bridge/internal/store"
)

func TestServerAdminLoginAndSessionLifecycle(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	cfg := config.Config{Mode: config.ModeServer, DataDir: dir, DatabaseURL: "postgres://configured", AgentToken: "agent-test-token", AdminPassword: "admin-test-password", InsecureCookie: true, CommandTimeout: time.Second}
	ts := httptest.NewServer(New(cfg, data).Handler)
	defer ts.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(ts.URL + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("unauthed page = %d, %q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(ts.URL + "/api/v1/targets")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthed API status = %d", response.StatusCode)
	}
	response, err = client.Get(ts.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login page status = %d", response.StatusCode)
	}

	login := func(origin, password string) *http.Response {
		t.Helper()
		request, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"`+password+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response = login("https://evil.example", "admin-test-password")
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin login status = %d", response.StatusCode)
	}
	response = login(ts.URL, "wrong-password")
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d", response.StatusCode)
	}
	response = login(ts.URL, "admin-test-password")
	response.Body.Close()
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 {
		t.Fatalf("login status = %d, cookies = %d", response.StatusCode, len(response.Cookies()))
	}
	cookie := response.Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Secure || cookie.MaxAge != int(adminSessionTTL.Seconds()) || cookie.Value == "" {
		t.Fatalf("invalid local test cookie attributes: %+v", cookie)
	}
	me := func(handler http.Handler) int {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		request.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request)
		return w.Code
	}
	if got := me(New(cfg, data).Handler); got != http.StatusUnauthorized {
		t.Fatalf("session survived restart: %d", got)
	}
	request, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/auth/me", nil)
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("logged-in me status = %d", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/targets", bytes.NewBufferString(`{}`))
	request.Header.Set("Origin", "https://evil.example")
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin management write status = %d", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/targets", bytes.NewBufferString(`{}`))
	request.Header.Set("Origin", ts.URL)
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("same-origin write status = %d", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/logout", nil)
	request.Header.Set("Origin", ts.URL)
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || len(response.Cookies()) != 1 || response.Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout did not expire cookie: %d, %+v", response.StatusCode, response.Cookies())
	}
	request, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/auth/me", nil)
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session remained active: %d", response.StatusCode)
	}
}

func TestServerAdminCookieSecureByDefault(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	cfg := config.Config{Mode: config.ModeServer, DataDir: dir, AdminPassword: "test-password", AgentToken: "token", CommandTimeout: time.Second}
	ts := httptest.NewServer(New(cfg, data).Handler)
	defer ts.Close()
	request, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	request.Header.Set("Origin", "https://"+request.URL.Host)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 || !response.Cookies()[0].Secure {
		t.Fatalf("secure cookie missing: status=%d, cookies=%+v", response.StatusCode, response.Cookies())
	}
}

func TestLocalModeDoesNotRequireLogin(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ts := httptest.NewServer(New(config.Config{Mode: config.ModeLocal, DataDir: dir}, data).Handler)
	defer ts.Close()
	response, err := http.Get(ts.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("local mode auth/me status = %d", response.StatusCode)
	}
	response, err = http.Get(ts.URL + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("local page status = %d", response.StatusCode)
	}
}
