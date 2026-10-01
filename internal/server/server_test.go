package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ssh-bridge/internal/config"
	"ssh-bridge/internal/store"
)

func TestServerKeyPathMustBeAbsolute(t *testing.T) {
	request := targetRequest{AuthMethod: "key", PrivateKeyPath: "keys/id_ed25519"}
	if got := (&App{cfg: config.Config{Mode: config.ModeServer}}).validateServerKeyPath(request); got == "" {
		t.Fatal("relative server key path was accepted")
	}
	request.PrivateKeyPath = "/run/ssh-keys/id_ed25519"
	if got := (&App{cfg: config.Config{Mode: config.ModeServer}}).validateServerKeyPath(request); got != "" {
		t.Fatalf("absolute server key path rejected: %s", got)
	}
	request.PrivateKeyPath = "keys/id_ed25519"
	if got := (&App{cfg: config.Config{Mode: config.ModeLocal}}).validateServerKeyPath(request); got != "" {
		t.Fatalf("local key path unexpectedly rejected: %s", got)
	}
}

func TestExecutionLifecycleReturnsRecoverableIDs(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	cfg := config.Config{Listen: "127.0.0.1:0", DataDir: dir, AgentToken: "agent-token", CommandTimeout: time.Second}
	ts := httptest.NewServer(New(cfg, data).Handler)
	defer ts.Close()
	if !json.Valid(openAPISpec) {
		t.Fatal("embedded OpenAPI document is not valid JSON")
	}
	specResponse, err := http.Get(ts.URL + "/api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer specResponse.Body.Close()
	if specResponse.StatusCode != http.StatusOK {
		t.Fatalf("OpenAPI status = %d", specResponse.StatusCode)
	}

	targetBody := bytes.NewBufferString(`{"name":"test","host":"127.0.0.1","port":22,"ssh_user":"root","private_key_path":"/definitely/missing","host_key_fingerprint":"","description":"","enabled":true}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/targets", targetBody)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create target status = %d", resp.StatusCode)
	}
	var target store.Target
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		t.Fatal(err)
	}
	agentTargetsRequest, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/agent/targets", nil)
	agentTargetsRequest.Header.Set("Authorization", "Bearer agent-token")
	agentTargetsResponse, err := http.DefaultClient.Do(agentTargetsRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer agentTargetsResponse.Body.Close()
	agentTargetsBody, err := io.ReadAll(agentTargetsResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(agentTargetsBody, []byte(target.ID)) || !bytes.Contains(agentTargetsBody, []byte(`"ssh_user":"root"`)) || bytes.Contains(agentTargetsBody, []byte("private_key_path")) {
		t.Fatalf("unexpected Agent target response: %s", agentTargetsBody)
	}
	updateBody := bytes.NewBufferString(`{"name":"test-updated","host":"127.0.0.1","port":22,"ssh_user":"root","private_key_path":"/definitely/missing","host_key_fingerprint":"","description":"updated","enabled":true}`)
	updateRequest, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/targets/"+target.ID, updateBody)
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse, err := http.DefaultClient.Do(updateRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer updateResponse.Body.Close()
	if updateResponse.StatusCode != http.StatusOK {
		t.Fatalf("update target status = %d", updateResponse.StatusCode)
	}
	var updated store.Target
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Name != "test-updated" || updated.Description != "updated" {
		t.Fatalf("target was not updated: %+v", updated)
	}
	testRequest, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/targets/"+target.ID+"/test", nil)
	testResponse, err := http.DefaultClient.Do(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer testResponse.Body.Close()
	var connectionTest struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(testResponse.Body).Decode(&connectionTest); err != nil {
		t.Fatal(err)
	}
	if connectionTest.Success || connectionTest.Message == "" {
		t.Fatalf("unexpected connection test response: %+v", connectionTest)
	}

	execBody := bytes.NewBufferString(`{"target_id":"` + target.ID + `","session_title":"测试会话","title":"读取状态","command":"uptime"}`)
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/executions", execBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer agent-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("execute status = %d", resp.StatusCode)
	}
	var accepted struct {
		SessionID   string `json:"session_id"`
		ExecutionID string `json:"execution_id"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.SessionID == "" || accepted.ExecutionID == "" || accepted.Status != "pending" {
		t.Fatalf("unexpected response: %+v", accepted)
	}

	waitURL := ts.URL + "/api/v1/sessions/" + accepted.SessionID + "/executions/" + accepted.ExecutionID + "/wait?timeout=2"
	req, _ = http.NewRequest(http.MethodGet, waitURL, nil)
	req.Header.Set("Authorization", "Bearer agent-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var execution store.Execution
	if err := json.NewDecoder(resp.Body).Decode(&execution); err != nil {
		t.Fatal(err)
	}
	if execution.Status != "failed" {
		t.Fatalf("status = %q, want failed", execution.Status)
	}

	var multipartBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&multipartBody)
	requestJSON := `{"target_id":"` + target.ID + `","session_title":"文件测试","title":"执行 SQL 文件","command":"cat {{file:sql}}"}`
	if err := multipartWriter.WriteField("request", requestJSON); err != nil {
		t.Fatal(err)
	}
	filePart, err := multipartWriter.CreateFormFile("sql", "query.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filePart.Write([]byte("SELECT 1;\n")); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	uploadRequest, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/executions", &multipartBody)
	uploadRequest.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	uploadRequest.Header.Set("Authorization", "Bearer agent-token")
	uploadResponse, err := http.DefaultClient.Do(uploadRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer uploadResponse.Body.Close()
	if uploadResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("multipart execute status = %d", uploadResponse.StatusCode)
	}
	var uploadAccepted struct {
		SessionID   string `json:"session_id"`
		ExecutionID string `json:"execution_id"`
	}
	if err := json.NewDecoder(uploadResponse.Body).Decode(&uploadAccepted); err != nil {
		t.Fatal(err)
	}
	uploadWaitURL := ts.URL + "/api/v1/sessions/" + uploadAccepted.SessionID + "/executions/" + uploadAccepted.ExecutionID + "/wait?timeout=2"
	uploadWaitRequest, _ := http.NewRequest(http.MethodGet, uploadWaitURL, nil)
	uploadWaitRequest.Header.Set("Authorization", "Bearer agent-token")
	uploadWaitResponse, err := http.DefaultClient.Do(uploadWaitRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer uploadWaitResponse.Body.Close()
	var uploadedExecution store.Execution
	if err := json.NewDecoder(uploadWaitResponse.Body).Decode(&uploadedExecution); err != nil {
		t.Fatal(err)
	}
	if len(uploadedExecution.Artifacts) != 1 || uploadedExecution.Artifacts[0].Placeholder != "sql" || uploadedExecution.Artifacts[0].OriginalName != "query.sql" {
		t.Fatalf("unexpected artifacts: %+v", uploadedExecution.Artifacts)
	}
	outputResponse, err := http.Get(ts.URL + "/api/v1/executions/" + uploadedExecution.ID + "/output")
	if err != nil {
		t.Fatal(err)
	}
	defer outputResponse.Body.Close()
	if outputResponse.StatusCode != http.StatusOK {
		t.Fatalf("download output status = %d", outputResponse.StatusCode)
	}
	downloadURL := ts.URL + "/api/v1/executions/" + uploadedExecution.ID + "/artifacts/" + uploadedExecution.Artifacts[0].ID
	downloadResponse, err := http.Get(downloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer downloadResponse.Body.Close()
	downloaded, err := io.ReadAll(downloadResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(downloaded) != "SELECT 1;\n" {
		t.Fatalf("downloaded artifact = %q", downloaded)
	}
}

func TestPasswordTargetAPINeverReturnsPassword(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	if err := data.ConfigureSecrets(dir); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(config.Config{DataDir: dir}, data).Handler)
	defer ts.Close()
	create := `{"name":"test","host":"127.0.0.1","port":22,"ssh_user":"operator","auth_method":"password","password":"example-secret"}`
	response, err := http.Post(ts.URL+"/api/v1/targets", "application/json", bytes.NewBufferString(create))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status: %d", response.StatusCode)
	}
	createBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(createBody, []byte("example-secret")) {
		t.Fatal("password leaked in create response")
	}
	var created store.Target
	if err := json.Unmarshal(createBody, &created); err != nil {
		t.Fatal(err)
	}
	if created.AuthMethod != "password" || !created.PasswordConfigured {
		t.Fatal("password target not configured")
	}
	update := `{"name":"renamed","host":"127.0.0.1","port":22,"ssh_user":"operator","auth_method":"password","password":""}`
	request, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/targets/"+created.ID, bytes.NewBufferString(update))
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update status: %d", response.StatusCode)
	}
	got, err := data.GetTarget(context.Background(), created.ID)
	if err != nil || got.Password != "example-secret" {
		t.Fatalf("password not preserved on edit: %v", err)
	}
	response, err = http.Get(ts.URL + "/api/v1/targets")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	listBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(listBody, []byte("example-secret")) {
		t.Fatal("password leaked in list response")
	}
}

func TestDeleteTargetHidesAgentAccessAndKeepsAudit(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	target := store.Target{ID: "target_delete_api", Name: "historical host", Host: "127.0.0.1", Port: 22, SSHUser: "operator", PrivateKeyPath: "/missing", Enabled: true}
	if err := data.SaveTarget(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	execution := store.Execution{ID: "execution_delete_api", SessionID: "session_delete_api", TargetID: target.ID, Command: "uptime", Status: "failed", CreatedAt: time.Now().UTC()}
	if err := data.CreateExecution(context.Background(), store.AgentSession{ID: execution.SessionID}, execution, nil, true); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(config.Config{DataDir: dir, AgentToken: "test-token"}, data).Handler)
	defer ts.Close()
	request, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/targets/"+target.ID, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status: %d", response.StatusCode)
	}
	response, err = http.Get(ts.URL + "/api/v1/targets")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var listed struct {
		Items []store.Target `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 0 {
		t.Fatal("deleted target remains in admin list")
	}
	request, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/agent/targets", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var agentListed struct {
		Items []store.Target `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&agentListed); err != nil {
		t.Fatal(err)
	}
	if len(agentListed.Items) != 0 {
		t.Fatal("deleted target remains in Agent list")
	}
	request, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/executions", bytes.NewBufferString(`{"target_id":"target_delete_api","command":"uptime"}`))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted target execution status: %d", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/sessions/"+execution.SessionID+"/executions/"+execution.ID, nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("historical execution status: %d", response.StatusCode)
	}
}

func TestAgentTokenSupportsCodexHeaderHelper(t *testing.T) {
	app := &App{agentToken: "agent-token"}

	bearer := httptest.NewRequest(http.MethodGet, "/", nil)
	bearer.Header.Set("Authorization", "Bearer agent-token")
	if !app.hasAgentToken(bearer) {
		t.Fatal("Bearer token should be accepted")
	}

	helper := httptest.NewRequest(http.MethodGet, "/", nil)
	helper.Header.Set("X-SSH-Bridge-Token", "agent-token")
	if !app.hasAgentToken(helper) {
		t.Fatal("Codex header helper token should be accepted")
	}

	invalid := httptest.NewRequest(http.MethodGet, "/", nil)
	invalid.Header.Set("X-SSH-Bridge-Token", "wrong-token")
	if app.hasAgentToken(invalid) {
		t.Fatal("invalid helper token should be rejected")
	}
}

func TestSystemInfoAndReadinessReflectRuntime(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Mode: config.ModeServer, Listen: "127.0.0.1:0", DataDir: dir, DatabaseURL: "postgres://configured", AgentToken: "agent-token", CommandTimeout: time.Second}
	ts := httptest.NewServer(New(cfg, data).Handler)
	defer ts.Close()

	response, err := http.Get(ts.URL + "/api/v1/system/info")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var info struct {
		Mode     string `json:"mode"`
		Database string `json:"database"`
	}
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Mode != config.ModeServer || info.Database != "postgresql" {
		t.Fatalf("system info = %+v", info)
	}

	ready, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	ready.Body.Close()
	if ready.StatusCode != http.StatusOK {
		t.Fatalf("ready status = %d", ready.StatusCode)
	}
	if err := data.Close(); err != nil {
		t.Fatal(err)
	}
	notReady, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer notReady.Body.Close()
	if notReady.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d", notReady.StatusCode)
	}
}

func TestNormalizeHostKeyFingerprint(t *testing.T) {
	// Generated solely for this test; it does not identify or authorize any real host.
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB9QHHO39SSF56xdwRYHb0wMHR4pasK/nuYhBJiyTsOn"
	const want = "SHA256:3Vh0AdOl3sPju9a1YU655VmxpgbVzFA9hESj78keTtQ"
	for _, input := range []string{key, "server.example " + key, want} {
		got, err := normalizeHostKeyFingerprint(input)
		if err != nil {
			t.Fatalf("normalize %q: %v", input, err)
		}
		if got != want {
			t.Fatalf("normalize %q = %q, want %q", input, got, want)
		}
	}
	if _, err := normalizeHostKeyFingerprint("not a host key"); err == nil {
		t.Fatal("invalid host key was accepted")
	}
	if got, err := normalizeHostKeyFingerprint(""); err != nil || got != "" {
		t.Fatalf("empty optional host key = %q, %v", got, err)
	}
	request := targetRequest{Name: "test", Host: "127.0.0.1", Port: 22, SSHUser: "root", PrivateKeyPath: "/tmp/key"}
	if message := validateTarget(request); message != "" {
		t.Fatalf("optional host key rejected: %s", message)
	}
}

func TestAgentTokenShownOnceAndRotationInvalidatesOldToken(t *testing.T) {
	dir := t.TempDir()
	data, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	cfg := config.Config{Listen: "127.0.0.1:0", DataDir: dir, AgentToken: "old-token", RevealAgentToken: true, CommandTimeout: time.Second}
	ts := httptest.NewServer(New(cfg, data).Handler)
	defer ts.Close()

	first, err := http.Get(ts.URL + "/api/v1/agent-token")
	if err != nil {
		t.Fatal(err)
	}
	var firstInfo struct {
		Token        string `json:"token"`
		TokenVisible bool   `json:"token_visible"`
	}
	if err := json.NewDecoder(first.Body).Decode(&firstInfo); err != nil {
		first.Body.Close()
		t.Fatal(err)
	}
	first.Body.Close()
	if !firstInfo.TokenVisible || firstInfo.Token != "old-token" {
		t.Fatalf("unexpected first token response: %+v", firstInfo)
	}

	second, err := http.Get(ts.URL + "/api/v1/agent-token")
	if err != nil {
		t.Fatal(err)
	}
	var secondInfo struct {
		Token        string `json:"token"`
		TokenVisible bool   `json:"token_visible"`
	}
	if err := json.NewDecoder(second.Body).Decode(&secondInfo); err != nil {
		second.Body.Close()
		t.Fatal(err)
	}
	second.Body.Close()
	if secondInfo.TokenVisible || secondInfo.Token != "" {
		t.Fatalf("token was shown more than once: %+v", secondInfo)
	}

	rotateRequest, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/agent-token/regenerate", nil)
	rotateResponse, err := http.DefaultClient.Do(rotateRequest)
	if err != nil {
		t.Fatal(err)
	}
	var rotated struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rotateResponse.Body).Decode(&rotated); err != nil {
		rotateResponse.Body.Close()
		t.Fatal(err)
	}
	rotateResponse.Body.Close()
	if rotated.Token == "" || rotated.Token == "old-token" {
		t.Fatalf("unexpected rotated token: %q", rotated.Token)
	}

	oldRequest, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/agent/targets", nil)
	oldRequest.Header.Set("Authorization", "Bearer old-token")
	oldResponse, err := http.DefaultClient.Do(oldRequest)
	if err != nil {
		t.Fatal(err)
	}
	oldResponse.Body.Close()
	if oldResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token status = %d", oldResponse.StatusCode)
	}
	newRequest, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/agent/targets", nil)
	newRequest.Header.Set("Authorization", "Bearer "+rotated.Token)
	newResponse, err := http.DefaultClient.Do(newRequest)
	if err != nil {
		t.Fatal(err)
	}
	newResponse.Body.Close()
	if newResponse.StatusCode != http.StatusOK {
		t.Fatalf("new token status = %d", newResponse.StatusCode)
	}
}
