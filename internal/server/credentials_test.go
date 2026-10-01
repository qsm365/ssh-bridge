package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ssh-bridge/internal/config"
	"ssh-bridge/internal/store"
)

func TestServerCredentialIsolationAndLifecycle(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	for _, id := range []string{"target_a", "target_b"} {
		if err := data.SaveTarget(t.Context(), store.Target{ID: id, Name: id, Host: "127.0.0.1", Port: 22, SSHUser: "test", PrivateKeyPath: "/missing", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{Mode: config.ModeServer, DataDir: dir, AdminPassword: "test-admin", InsecureCookie: true, CommandTimeout: time.Second, AgentToken: "old-global-token"}
	ts := httptest.NewServer(New(cfg, data).Handler)
	defer ts.Close()
	request := func(method, path, body, token string, cookie *http.Cookie) (*http.Response, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if method != http.MethodGet {
			req.Header.Set("Origin", ts.URL)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		result := map[string]any{}
		if resp.StatusCode != 204 {
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
		}
		return resp, result
	}
	loginReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"test-admin"}`))
	loginReq.Header.Set("Origin", ts.URL)
	loginResp, err := http.DefaultClient.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != 200 {
		t.Fatalf("login=%d", loginResp.StatusCode)
	}
	cookie := loginResp.Cookies()[0]
	create := func(name, target string) (string, string) {
		t.Helper()
		body := `{"name":"` + name + `","target_ids":["` + target + `"]}`
		resp, value := request("POST", "/api/v1/agent-credentials", body, "", cookie)
		if resp.StatusCode != 201 {
			t.Fatalf("create=%d %v", resp.StatusCode, value)
		}
		return value["id"].(string), value["token"].(string)
	}
	idA, tokenA := create("Agent A", "target_a")
	_, tokenB := create("Agent B", "target_b")
	if resp, _ := request("GET", "/api/v1/agent/targets", "", "old-global-token", nil); resp.StatusCode != 401 {
		t.Fatalf("global token=%d", resp.StatusCode)
	}
	if resp, value := request("GET", "/api/v1/agent/targets", "", tokenA, nil); resp.StatusCode != 200 || len(value["items"].([]any)) != 1 || value["items"].([]any)[0].(map[string]any)["id"] != "target_a" {
		t.Fatalf("A targets=%d %v", resp.StatusCode, value)
	}
	if resp, value := request("GET", "/api/v1/agent/targets", "", tokenB, nil); resp.StatusCode != 200 || len(value["items"].([]any)) != 1 || value["items"].([]any)[0].(map[string]any)["id"] != "target_b" {
		t.Fatalf("B targets=%d %v", resp.StatusCode, value)
	}
	mcpList := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_targets","arguments":{}}}`
	if resp, value := request("POST", "/mcp", mcpList, tokenA, nil); resp.StatusCode != 200 || len(value["result"].(map[string]any)["structuredContent"].(map[string]any)["items"].([]any)) != 1 || value["result"].(map[string]any)["structuredContent"].(map[string]any)["items"].([]any)[0].(map[string]any)["id"] != "target_a" {
		t.Fatalf("MCP targets=%d %v", resp.StatusCode, value)
	}
	if resp, _ := request("POST", "/api/v1/executions", `{"target_id":"target_b","command":"true"}`, tokenA, nil); resp.StatusCode != 403 {
		t.Fatalf("cross target execute=%d", resp.StatusCode)
	}
	resp, accepted := request("POST", "/api/v1/executions", `{"target_id":"target_a","command":"true"}`, tokenA, nil)
	if resp.StatusCode != 202 {
		t.Fatalf("execute=%d %v", resp.StatusCode, accepted)
	}
	sessionID, executionID := accepted["session_id"].(string), accepted["execution_id"].(string)
	path := "/api/v1/sessions/" + sessionID + "/executions/" + executionID
	for _, toolName := range []string{"get_execution", "wait_execution", "download_output"} {
		mcpCall := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + toolName + `","arguments":{"session_id":"` + sessionID + `","execution_id":"` + executionID + `"}}}`
		if resp, value := request("POST", "/mcp", mcpCall, tokenB, nil); resp.StatusCode != 200 || value["result"].(map[string]any)["isError"] != true {
			t.Fatalf("cross MCP %s=%d %v", toolName, resp.StatusCode, value)
		}
	}
	for _, suffix := range []string{"", "/wait?timeout=1", "/output"} {
		if resp, _ := request("GET", path+suffix, "", tokenB, nil); resp.StatusCode != 404 {
			t.Fatalf("cross credential %s=%d", suffix, resp.StatusCode)
		}
	}
	if resp, _ := request("POST", "/api/v1/executions", `{"session_id":"`+sessionID+`","target_id":"target_b","command":"true"}`, tokenB, nil); resp.StatusCode != 404 {
		t.Fatalf("cross session execute=%d", resp.StatusCode)
	}
	if resp, _ := request("PUT", "/api/v1/agent-credentials/"+idA, `{"name":"Agent A","target_ids":[],"enabled":true}`, "", cookie); resp.StatusCode != 200 {
		t.Fatalf("remove access=%d", resp.StatusCode)
	}
	if resp, _ := request("POST", "/api/v1/executions", `{"target_id":"target_a","command":"true"}`, tokenA, nil); resp.StatusCode != 403 {
		t.Fatalf("revoked execute=%d", resp.StatusCode)
	}
	if resp, _ := request("GET", path, "", tokenA, nil); resp.StatusCode != 200 {
		t.Fatalf("history after revoke=%d", resp.StatusCode)
	}
	resp, rotated := request("POST", "/api/v1/agent-credentials/"+idA+"/regenerate", `{}`, "", cookie)
	if resp.StatusCode != 200 {
		t.Fatalf("rotate=%d", resp.StatusCode)
	}
	newToken := rotated["token"].(string)
	if resp, _ := request("GET", path, "", tokenA, nil); resp.StatusCode != 401 {
		t.Fatalf("old token=%d", resp.StatusCode)
	}
	if resp, _ := request("GET", path, "", newToken, nil); resp.StatusCode != 200 {
		t.Fatalf("new token history=%d", resp.StatusCode)
	}
	if resp, _ := request("PUT", "/api/v1/agent-credentials/"+idA, `{"name":"Agent A","target_ids":[],"enabled":false}`, "", cookie); resp.StatusCode != 200 {
		t.Fatalf("disable=%d", resp.StatusCode)
	}
	if resp, _ := request("GET", path, "", newToken, nil); resp.StatusCode != 401 {
		t.Fatalf("disabled token=%d", resp.StatusCode)
	}
	resp, list := request("GET", "/api/v1/agent-credentials", "", "", cookie)
	encoded, _ := json.Marshal(list)
	if resp.StatusCode != 200 || bytes.Contains(encoded, []byte(newToken)) || bytes.Contains(encoded, []byte(tokenB)) {
		t.Fatalf("credential list leaked token: %d", resp.StatusCode)
	}
	adminSession, err := data.GetSession(t.Context(), sessionID)
	if err != nil || adminSession.AgentCredentialID != idA || adminSession.AgentCredentialName != "Agent A" {
		t.Fatalf("audit owner: %+v %v", adminSession, err)
	}
	adminExecution, err := data.GetExecution(t.Context(), sessionID, executionID)
	if err != nil || adminExecution.AgentCredentialName != "Agent A" {
		t.Fatalf("execution owner: %+v %v", adminExecution, err)
	}
}
