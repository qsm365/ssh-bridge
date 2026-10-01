package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ssh-bridge/internal/config"
)

const (
	adminCookieName = "ssh_bridge_admin"
	adminSessionTTL = 12 * time.Hour
)

func (a *App) sameOrigin(r *http.Request) bool {
	value := r.Header.Get("Origin")
	origin, err := url.Parse(value)
	if err != nil || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.User != nil {
		return false
	}
	scheme := "https"
	if a.cfg.InsecureCookie && r.TLS == nil {
		scheme = "http"
	}
	return origin.Scheme == scheme && strings.EqualFold(origin.Host, r.Host)
}

func (a *App) hasAdminSession(r *http.Request) bool {
	if a.cfg.Mode != config.ModeServer {
		return true
	}
	cookie, err := r.Cookie(adminCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	expires, found := a.sessions[cookie.Value]
	if !found {
		return false
	}
	if !time.Now().Before(expires) {
		delete(a.sessions, cookie.Value)
		return false
	}
	return true
}

func (a *App) sessionCookie(value string, expires time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: adminCookieName, Value: value, Path: "/", Expires: expires,
		MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: !a.cfg.InsecureCookie,
	}
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.cfg.Mode != config.ModeServer {
		apiError(w, http.StatusNotFound, "login is not required in local mode")
		return
	}
	if !a.sameOrigin(r) {
		apiError(w, http.StatusForbidden, "same-origin request required")
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &request); err != nil {
		apiError(w, http.StatusBadRequest, "invalid request")
		return
	}
	hash := sha256.Sum256([]byte(request.Password))
	if request.Username != "admin" || subtle.ConstantTimeCompare(hash[:], a.adminHash[:]) != 1 {
		apiError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		apiError(w, http.StatusInternalServerError, "create session failed")
		return
	}
	value := base64.RawURLEncoding.EncodeToString(token)
	expires := time.Now().Add(adminSessionTTL)
	a.sessionMu.Lock()
	for session, deadline := range a.sessions {
		if !time.Now().Before(deadline) {
			delete(a.sessions, session)
		}
	}
	a.sessions[value] = expires
	a.sessionMu.Unlock()
	http.SetCookie(w, a.sessionCookie(value, expires, int(adminSessionTTL.Seconds())))
	jsonResponse(w, http.StatusOK, map[string]string{"username": "admin"})
}

func (a *App) me(w http.ResponseWriter, _ *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]string{"username": "admin"})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Mode == config.ModeServer {
		if cookie, err := r.Cookie(adminCookieName); err == nil {
			a.sessionMu.Lock()
			delete(a.sessions, cookie.Value)
			a.sessionMu.Unlock()
		}
		http.SetCookie(w, a.sessionCookie("", time.Unix(0, 0), -1))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !a.hasAdminSession(r) {
			apiError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if a.cfg.Mode == config.ModeServer && r.Method != http.MethodGet && r.Method != http.MethodHead && !a.sameOrigin(r) {
			apiError(w, http.StatusForbidden, "same-origin request required")
			return
		}
		next(w, r)
	}
}

func (a *App) adminPage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.Mode == config.ModeServer && r.URL.Path != "/login" && !strings.HasPrefix(r.URL.Path, "/assets/") && !a.hasAdminSession(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
