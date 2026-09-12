package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ssh-bridge/internal/config"
	"ssh-bridge/internal/store"
)

func TestMCPToolsAndExecutionLifecycle(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	if err := data.SaveTarget(t.Context(), store.Target{
		ID: "target_test", Name: "MCP Test", Host: "127.0.0.1", Port: 22, SSHUser: "root",
		PrivateKeyPath: "/definitely/missing", Description: "safe summary", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Listen: "127.0.0.1:0", DataDir: dir, AgentToken: "agent-token", CommandTimeout: time.Second}
	ts := httptest.NewServer(New(cfg, data).Handler)
	defer ts.Close()

	unauthorized, err := http.Post(ts.URL+"/mcp", "application/json", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized MCP status = %d", unauthorized.StatusCode)
	}

	initialize := callMCP(t, ts.URL, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	if initialize.Error != nil || initialize.Result["protocolVersion"] != "2025-11-25" {
		t.Fatalf("unexpected initialize response: %+v", initialize)
	}
	if initialize.Result["instructions"] != mcpInstructions {
		t.Fatalf("unexpected MCP instructions: %v", initialize.Result["instructions"])
	}

	listed := callMCP(t, ts.URL, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	tools, ok := listed.Result["tools"].([]any)
	if !ok || len(tools) != 5 {
		t.Fatalf("unexpected tools/list response: %+v", listed.Result)
	}
	annotations := make(map[string]map[string]any)
	for _, item := range tools {
		tool := item.(map[string]any)
		annotations[tool["name"].(string)] = tool["annotations"].(map[string]any)
	}
	if annotations["list_targets"]["readOnlyHint"] != true || annotations["execute_command"]["readOnlyHint"] != false {
		t.Fatalf("unexpected tool annotations: %+v", annotations)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte("SELECT 1;\n"))
	executeBody := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"execute_command","arguments":{"target_id":"target_test","session_title":"MCP session","title":"MCP execution","command":"cat {{file:sql}}","files":[{"name":"sql","filename":"query.sql","content_base64":"` + encoded + `"}]}}}`
	executed := callMCP(t, ts.URL, executeBody)
	accepted := structuredMap(t, executed)
	sessionID, _ := accepted["session_id"].(string)
	executionID, _ := accepted["execution_id"].(string)
	if sessionID == "" || executionID == "" || accepted["status"] != "pending" {
		t.Fatalf("unexpected execute result: %+v", accepted)
	}

	waitBody := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"wait_execution","arguments":{"session_id":"` + sessionID + `","execution_id":"` + executionID + `","timeout_seconds":2}}}`
	waited := structuredMap(t, callMCP(t, ts.URL, waitBody))
	if waited["status"] != "failed" {
		t.Fatalf("execution status = %v, want failed", waited["status"])
	}
	artifacts, ok := waited["artifacts"].([]any)
	if !ok || len(artifacts) != 1 {
		t.Fatalf("unexpected MCP artifacts: %+v", waited["artifacts"])
	}
}

type decodedMCPResponse struct {
	Result map[string]any `json:"result"`
	Error  *mcpError      `json:"error"`
}

func callMCP(t *testing.T, serverURL, body string) decodedMCPResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, serverURL+"/mcp", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer agent-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("MCP status = %d", response.StatusCode)
	}
	var decoded decodedMCPResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func structuredMap(t *testing.T, response decodedMCPResponse) map[string]any {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("MCP error: %+v", response.Error)
	}
	if response.Result["isError"] == true {
		t.Fatalf("MCP tool error: %+v", response.Result)
	}
	structured, ok := response.Result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredContent: %+v", response.Result)
	}
	return structured
}

func TestMCPRejectsRemoteBrowserOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()
	app := &App{cfg: config.Config{AgentToken: "token"}, agentToken: "token"}
	app.authorized(app.mcp)(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("remote Origin status = %d", recorder.Code)
	}
}
