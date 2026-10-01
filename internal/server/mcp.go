package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"ssh-bridge/internal/store"
)

const mcpProtocolVersion = "2026-07-28"
const mcpInstructions = "All remote-host operations must use ssh_bridge. Before the first operation on a target, call list_targets and select a target_id returned by it. If the requested target is not listed, stop: do not use ssh, scp, sftp, rsync, mosh, another MCP server, or any alternative direct connection. Tell the user that the target is not currently supported and must be added in the SSH Bridge service. Do not infer or invent a target_id. execute_command returns session_id and execution_id; reuse session_id for later commands in the same user conversation. Normally call wait_execution once. After a network interruption, call get_execution before considering a retry so the command is not executed twice. Use download_output only when the preview is insufficient. Command approval remains the responsibility of the calling Agent."

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

type mcpToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type mcpFileInput struct {
	Name          string `json:"name"`
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
}

type mcpExecuteInput struct {
	SessionID    string         `json:"session_id"`
	TargetID     string         `json:"target_id"`
	Title        string         `json:"title"`
	SessionTitle string         `json:"session_title"`
	Command      string         `json:"command"`
	WorkingDir   string         `json:"working_dir"`
	Files        []mcpFileInput `json:"files"`
}

func (a *App) mcp(w http.ResponseWriter, r *http.Request) {
	if !validMCPOrigin(r.Header.Get("Origin")) {
		apiError(w, http.StatusForbidden, "origin is not allowed")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		apiError(w, http.StatusMethodNotAllowed, "MCP endpoint accepts POST requests")
		return
	}
	w.Header().Set("MCP-Protocol-Version", mcpProtocolVersion)
	var request mcpRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 40<<20))
	if err := decoder.Decode(&request); err != nil || request.JSONRPC != "2.0" || request.Method == "" {
		a.writeMCPError(w, nil, -32600, "invalid JSON-RPC request")
		return
	}
	if methodHeader := r.Header.Get("Mcp-Method"); methodHeader != "" && methodHeader != request.Method {
		a.writeMCPError(w, request.ID, -32600, "Mcp-Method header does not match request method")
		return
	}
	if request.Method == "tools/call" {
		var call mcpToolCall
		if err := json.Unmarshal(request.Params, &call); err != nil || call.Name == "" {
			a.writeMCPError(w, request.ID, -32602, "invalid tools/call parameters")
			return
		}
		if nameHeader := r.Header.Get("Mcp-Name"); nameHeader != "" && nameHeader != call.Name {
			a.writeMCPError(w, request.ID, -32600, "Mcp-Name header does not match tool name")
			return
		}
	}

	if len(request.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch request.Method {
	case "initialize":
		// Compatibility for clients using the pre-2026 handshake.
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		version := params.ProtocolVersion
		if version == "" {
			version = "2025-11-25"
		}
		a.writeMCPResult(w, request.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]string{"name": "ssh-bridge", "version": "0.1.0"},
			"instructions":    mcpInstructions,
		})
	case "server/discover":
		a.writeMCPResult(w, request.ID, map[string]any{
			"resultType":   "complete",
			"capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":   map[string]string{"name": "ssh-bridge", "version": "0.1.0"},
			"instructions": mcpInstructions,
		})
	case "ping":
		a.writeMCPResult(w, request.ID, map[string]any{})
	case "tools/list":
		a.writeMCPResult(w, request.ID, map[string]any{
			"resultType": "complete", "tools": mcpTools(), "ttlMs": 300000, "cacheScope": "private",
		})
	case "tools/call":
		var call mcpToolCall
		_ = json.Unmarshal(request.Params, &call)
		result, err := a.callMCPTool(r.Context(), call)
		if err != nil {
			a.writeMCPResult(w, request.ID, mcpToolResult(map[string]any{"error": err.Error()}, true))
			return
		}
		a.writeMCPResult(w, request.ID, mcpToolResult(result, false))
	default:
		a.writeMCPError(w, request.ID, -32601, "method not found")
	}
}

func validMCPOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (a *App) writeMCPResult(w http.ResponseWriter, id json.RawMessage, result any) {
	jsonResponse(w, http.StatusOK, mcpResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (a *App) writeMCPError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	jsonResponse(w, http.StatusOK, mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: message}})
}

func mcpToolResult(value any, isError bool) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{
		"resultType":        "complete",
		"content":           []map[string]string{{"type": "text", "text": string(encoded)}},
		"structuredContent": value,
		"isError":           isError,
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func mcpTools() []mcpTool {
	stringProperty := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	readOnly := map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false}
	sshWrite := map[string]any{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true}
	return []mcpTool{
		{Name: "list_targets", Title: "列出目标主机", Description: "列出当前允许 Agent 使用的目标主机及 SSH 用户名，不返回密码或私钥。", InputSchema: objectSchema(map[string]any{}), Annotations: readOnly},
		{Name: "execute_command", Title: "执行 SSH 命令", Description: "异步发起 SSH 命令。首次调用不传 session_id；后续同一对话复用返回的 session_id。文件只能通过 {{file:name}} 占位符引用。", InputSchema: objectSchema(map[string]any{
			"session_id": stringProperty("已有的 SSH Bridge 会话 ID，可选"), "target_id": stringProperty("目标主机 ID"),
			"session_title": stringProperty("会话标题，可选"), "title": stringProperty("本次执行标题，可选"),
			"command": stringProperty("需要执行的命令"), "working_dir": stringProperty("远端工作目录，可选"),
			"files": map[string]any{"type": "array", "maxItems": 8, "items": objectSchema(map[string]any{
				"name": stringProperty("占位符名称"), "filename": stringProperty("审计记录中的文件名"), "content_base64": stringProperty("文件内容的 Base64 编码"),
			}, "name", "filename", "content_base64")},
		}, "target_id", "command"), Annotations: sshWrite},
		{Name: "get_execution", Title: "查询执行结果", Description: "通过会话 ID 和执行 ID 恢复查询命令状态及输出预览。", InputSchema: objectSchema(map[string]any{
			"session_id": stringProperty("会话 ID"), "execution_id": stringProperty("执行 ID"),
		}, "session_id", "execution_id"), Annotations: readOnly},
		{Name: "wait_execution", Title: "等待执行结果", Description: "长等待命令进入终态，最长 60 秒；超时后返回当前状态。", InputSchema: objectSchema(map[string]any{
			"session_id": stringProperty("会话 ID"), "execution_id": stringProperty("执行 ID"),
			"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 60, "default": 55},
		}, "session_id", "execution_id"), Annotations: readOnly},
		{Name: "download_output", Title: "读取完整执行输出", Description: "分块读取归档的 NDJSON 输出。返回 Base64 数据、下一偏移量和是否读完。", InputSchema: objectSchema(map[string]any{
			"session_id": stringProperty("会话 ID"), "execution_id": stringProperty("执行 ID"),
			"offset": map[string]any{"type": "integer", "minimum": 0, "default": 0},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 1048576, "default": 262144},
		}, "session_id", "execution_id"), Annotations: readOnly},
	}
}

func decodeMCPArguments(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return errors.New("invalid tool arguments")
	}
	return nil
}

func (a *App) callMCPTool(ctx context.Context, call mcpToolCall) (any, error) {
	switch call.Name {
	case "list_targets":
		var input struct{}
		if err := decodeMCPArguments(call.Arguments, &input); err != nil {
			return nil, err
		}
		targets, err := a.store.ListTargets(ctx)
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(targets))
		for _, target := range targets {
			if target.Enabled {
				if credentialID := credentialFromContext(ctx); credentialID != "" {
					allowed, err := a.store.CredentialCanAccessTarget(ctx, credentialID, target.ID)
					if err != nil {
						return nil, err
					}
					if !allowed {
						continue
					}
				}
				items = append(items, map[string]any{"id": target.ID, "name": target.Name, "ssh_user": target.SSHUser, "description": target.Description})
			}
		}
		return map[string]any{"items": items}, nil
	case "execute_command":
		var input mcpExecuteInput
		if err := decodeMCPArguments(call.Arguments, &input); err != nil {
			return nil, err
		}
		uploads, err := decodeMCPFiles(input.Command, input.Files)
		if err != nil {
			return nil, err
		}
		accepted, _, err := a.startExecution(ctx, executionRequest{
			SessionID: input.SessionID, TargetID: input.TargetID, Title: input.Title, SessionTitle: input.SessionTitle,
			Command: input.Command, WorkingDir: input.WorkingDir,
		}, uploads)
		return accepted, err
	case "get_execution":
		var input struct {
			SessionID   string `json:"session_id"`
			ExecutionID string `json:"execution_id"`
		}
		if err := decodeMCPArguments(call.Arguments, &input); err != nil {
			return nil, err
		}
		return a.loadExecution(ctx, input.SessionID, input.ExecutionID)
	case "wait_execution":
		var input struct {
			SessionID      string `json:"session_id"`
			ExecutionID    string `json:"execution_id"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if err := decodeMCPArguments(call.Arguments, &input); err != nil {
			return nil, err
		}
		if input.TimeoutSeconds == 0 {
			input.TimeoutSeconds = 55
		}
		if input.TimeoutSeconds < 1 || input.TimeoutSeconds > 60 {
			return nil, errors.New("timeout_seconds must be between 1 and 60")
		}
		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(input.TimeoutSeconds)*time.Second)
		defer cancel()
		for {
			execution, err := a.loadExecution(ctx, input.SessionID, input.ExecutionID)
			if err != nil || terminal(execution.Status) {
				return execution, err
			}
			select {
			case <-waitCtx.Done():
				return execution, nil
			case <-time.After(200 * time.Millisecond):
			}
		}
	case "download_output":
		return a.readMCPOutput(ctx, call.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
}

func decodeMCPFiles(command string, files []mcpFileInput) ([]uploadedFile, error) {
	if len(files) > 8 {
		return nil, errors.New("at most 8 files may be uploaded")
	}
	provided := make(map[string]bool)
	uploads := make([]uploadedFile, 0, len(files))
	namePattern := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	for _, file := range files {
		if !namePattern.MatchString(file.Name) || provided[file.Name] {
			return nil, fmt.Errorf("invalid or duplicate file placeholder %q", file.Name)
		}
		if file.Filename == "" {
			return nil, fmt.Errorf("filename is required for placeholder %q", file.Name)
		}
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("file %q content_base64 is invalid", file.Name)
		}
		if len(content) > 16<<20 {
			return nil, fmt.Errorf("file %q exceeds 16 MiB limit", file.Filename)
		}
		provided[file.Name] = true
		uploads = append(uploads, uploadedFile{Placeholder: file.Name, Filename: file.Filename, Content: content})
	}
	required := make(map[string]bool)
	for _, match := range placeholderPattern.FindAllStringSubmatch(command, -1) {
		required[match[1]] = true
	}
	if strings.Contains(placeholderPattern.ReplaceAllString(command, ""), "{{file:") {
		return nil, errors.New("command contains an invalid file placeholder")
	}
	for name := range required {
		if !provided[name] {
			return nil, fmt.Errorf("placeholder %q has no uploaded file", name)
		}
	}
	for name := range provided {
		if !required[name] {
			return nil, fmt.Errorf("uploaded file %q is not referenced by the command", name)
		}
	}
	return uploads, nil
}

func (a *App) loadExecution(ctx context.Context, sessionID, executionID string) (store.Execution, error) {
	if sessionID == "" || executionID == "" {
		return store.Execution{}, errors.New("session_id and execution_id are required")
	}
	if a.cfg.Mode == "server" {
		owned, err := a.store.SessionOwnedBy(ctx, sessionID, credentialFromContext(ctx))
		if err != nil {
			return store.Execution{}, err
		}
		if !owned {
			return store.Execution{}, errors.New("execution not found")
		}
	}
	execution, err := a.store.GetExecution(ctx, sessionID, executionID)
	if errors.Is(err, store.ErrNotFound) {
		return execution, errors.New("execution not found")
	}
	if err != nil {
		return execution, err
	}
	if err := a.attachArtifacts(ctx, &execution); err != nil {
		return execution, err
	}
	return execution, nil
}

func (a *App) readMCPOutput(ctx context.Context, raw json.RawMessage) (any, error) {
	var input struct {
		SessionID   string `json:"session_id"`
		ExecutionID string `json:"execution_id"`
		Offset      int64  `json:"offset"`
		Limit       int    `json:"limit"`
	}
	if err := decodeMCPArguments(raw, &input); err != nil {
		return nil, err
	}
	if input.Offset < 0 {
		return nil, errors.New("offset must not be negative")
	}
	if input.Limit == 0 {
		input.Limit = 256 << 10
	}
	if input.Limit < 1 || input.Limit > 1<<20 {
		return nil, errors.New("limit must be between 1 and 1048576")
	}
	execution, err := a.loadExecution(ctx, input.SessionID, input.ExecutionID)
	if err != nil {
		return nil, err
	}
	if execution.OutputPath == "" {
		return nil, errors.New("output is not available yet")
	}
	file, err := os.Open(execution.OutputPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if input.Offset > info.Size() {
		return nil, errors.New("offset exceeds output size")
	}
	if _, err := file.Seek(input.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	buffer := make([]byte, input.Limit)
	read, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	next := input.Offset + int64(read)
	return map[string]any{
		"data_base64": base64.StdEncoding.EncodeToString(buffer[:read]), "offset": input.Offset,
		"next_offset": next, "total_size": info.Size(), "eof": next >= info.Size(),
	}, nil
}
