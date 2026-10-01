package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"ssh-bridge/internal/config"
	"ssh-bridge/internal/executor"
	"ssh-bridge/internal/id"
	"ssh-bridge/internal/store"
	webui "ssh-bridge/web"
)

type App struct {
	cfg          config.Config
	store        *store.Store
	runner       *executor.Runner
	adminHash    [32]byte
	sessionMu    sync.Mutex
	sessions     map[string]time.Time
	tokenMu      sync.RWMutex
	agentToken   string
	oneTimeToken string
}

type executionRequest struct {
	SessionID    string `json:"session_id"`
	TargetID     string `json:"target_id"`
	Title        string `json:"title"`
	SessionTitle string `json:"session_title"`
	Command      string `json:"command"`
	WorkingDir   string `json:"working_dir"`
}

type uploadedFile struct {
	Placeholder string
	Header      *multipart.FileHeader
	Filename    string
	Content     []byte
}

type acceptedExecution struct {
	SessionID   string `json:"session_id"`
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
}

var placeholderPattern = regexp.MustCompile(`\{\{file:([A-Za-z0-9_-]{1,64})\}\}`)

//go:embed openapi.json
var openAPISpec []byte

func New(cfg config.Config, data *store.Store) *http.Server {
	if cfg.Mode == "" {
		cfg.Mode = config.ModeLocal
	}
	adminHash := sha256.Sum256([]byte(cfg.AdminPassword))
	cfg.AdminPassword = ""
	a := &App{cfg: cfg, store: data, runner: &executor.Runner{Store: data, OutputDir: cfg.OutputDir(), Timeout: cfg.CommandTimeout}, adminHash: adminHash, sessions: make(map[string]time.Time), agentToken: cfg.AgentToken}
	if cfg.RevealAgentToken {
		a.oneTimeToken = cfg.AgentToken
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("GET /api/v1/system/info", a.systemInfo)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("GET /api/v1/auth/me", a.admin(a.me))
	mux.HandleFunc("POST /api/v1/auth/logout", a.admin(a.logout))
	mux.HandleFunc("GET /api/v1/agent-token", a.admin(a.getAgentToken))
	mux.HandleFunc("POST /api/v1/agent-token/regenerate", a.admin(a.regenerateAgentToken))
	mux.HandleFunc("GET /api/v1/agent-credentials", a.admin(a.listAgentCredentials))
	mux.HandleFunc("POST /api/v1/agent-credentials", a.admin(a.createAgentCredential))
	mux.HandleFunc("PUT /api/v1/agent-credentials/{id}", a.admin(a.updateAgentCredential))
	mux.HandleFunc("POST /api/v1/agent-credentials/{id}/regenerate", a.admin(a.regenerateAgentCredential))
	mux.HandleFunc("GET /api/openapi.json", a.openAPI)
	mux.HandleFunc("/mcp", a.authorized(a.mcp))
	mux.HandleFunc("GET /api/v1/agent/targets", a.authorized(a.listAgentTargets))
	mux.HandleFunc("GET /api/v1/targets", a.admin(a.listTargets))
	mux.HandleFunc("POST /api/v1/targets", a.admin(a.createTarget))
	mux.HandleFunc("POST /api/v1/targets/test", a.admin(a.testTargetInput))
	mux.HandleFunc("POST /api/v1/targets/{id}/test", a.admin(a.testSavedTarget))
	mux.HandleFunc("PUT /api/v1/targets/{id}", a.admin(a.updateTarget))
	mux.HandleFunc("DELETE /api/v1/targets/{id}", a.admin(a.deleteTarget))
	mux.HandleFunc("GET /api/v1/sessions", a.admin(a.listSessions))
	mux.HandleFunc("GET /api/v1/sessions/{id}", a.admin(a.getSession))
	mux.HandleFunc("GET /api/v1/executions", a.admin(a.listExecutions))
	mux.HandleFunc("GET /api/v1/executions/{executionID}/output", a.admin(a.getAdminExecutionOutput))
	mux.HandleFunc("GET /api/v1/executions/{executionID}/artifacts/{artifactID}", a.admin(a.downloadArtifact))
	mux.HandleFunc("POST /api/v1/executions", a.authorized(a.createExecution))
	mux.HandleFunc("GET /api/v1/sessions/{sessionID}/executions/{executionID}", a.authorized(a.getExecution))
	mux.HandleFunc("GET /api/v1/sessions/{sessionID}/executions/{executionID}/wait", a.authorized(a.waitExecution))
	mux.HandleFunc("GET /api/v1/sessions/{sessionID}/executions/{executionID}/output", a.authorized(a.getExecutionOutput))
	mux.Handle("/", a.adminPage(spaHandler()))
	return &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 75 * time.Second}
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
func (a *App) systemInfo(w http.ResponseWriter, _ *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]any{"mode": a.cfg.Mode, "database": a.cfg.DatabaseName(), "mock": false, "openapi_url": "/api/openapi.json", "mcp_url": "/mcp"})
}

func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		apiError(w, http.StatusServiceUnavailable, "database is not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (a *App) openAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(openAPISpec)
}

func secureEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (a *App) hasAgentToken(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	value := ""
	if strings.HasPrefix(header, "Bearer ") {
		value = strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	} else {
		value = strings.TrimSpace(r.Header.Get("X-SSH-Bridge-Token"))
	}
	a.tokenMu.RLock()
	current := a.agentToken
	a.tokenMu.RUnlock()
	return value != "" && secureEqual(value, current)
}

func (a *App) getAgentToken(w http.ResponseWriter, _ *http.Request) {
	if a.cfg.Mode == config.ModeServer {
		apiError(w, http.StatusNotFound, "local token is unavailable in server mode")
		return
	}
	a.tokenMu.Lock()
	token := a.oneTimeToken
	a.oneTimeToken = ""
	a.tokenMu.Unlock()
	jsonResponse(w, http.StatusOK, map[string]any{
		"configured": true, "token": token, "token_visible": token != "", "mcp_url": "/mcp",
	})
}

func (a *App) regenerateAgentToken(w http.ResponseWriter, _ *http.Request) {
	if a.cfg.Mode == config.ModeServer {
		apiError(w, http.StatusNotFound, "local token is unavailable in server mode")
		return
	}
	token, err := config.ReplaceAgentToken(a.cfg.DataDir)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.tokenMu.Lock()
	a.agentToken = token
	a.oneTimeToken = ""
	a.tokenMu.Unlock()
	jsonResponse(w, http.StatusOK, map[string]any{
		"configured": true, "token": token, "token_visible": true, "mcp_url": "/mcp",
	})
}
func (a *App) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.Mode == config.ModeServer {
			credentialID, err := a.authenticateCredential(r)
			if err != nil {
				apiError(w, http.StatusInternalServerError, "credential lookup failed")
				return
			}
			if credentialID == "" {
				apiError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			next(w, r.WithContext(context.WithValue(r.Context(), credentialContextKey{}, credentialID)))
			return
		}
		if !a.hasAgentToken(r) {
			apiError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next(w, r)
	}
}

type targetRequest struct {
	Name               string `json:"name"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	SSHUser            string `json:"ssh_user"`
	AuthMethod         string `json:"auth_method"`
	PrivateKeyPath     string `json:"private_key_path"`
	Password           string `json:"password"`
	ExistingTargetID   string `json:"existing_target_id"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
	Description        string `json:"description"`
	Enabled            *bool  `json:"enabled"`
}

func validateTarget(r targetRequest) string {
	if strings.TrimSpace(r.Name) == "" {
		return "name is required"
	}
	return validateTargetConnection(r)
}

func validateTargetConnection(r targetRequest) string {
	if strings.TrimSpace(r.Host) == "" {
		return "host is required"
	}
	if r.Port < 1 || r.Port > 65535 {
		return "port must be between 1 and 65535"
	}
	if strings.TrimSpace(r.SSHUser) == "" {
		return "ssh_user is required"
	}
	if r.AuthMethod != "" && r.AuthMethod != "key" && r.AuthMethod != "password" {
		return "auth_method must be key or password"
	}
	if r.AuthMethod == "password" {
		if r.Password == "" {
			return "password is required"
		}
	} else if strings.TrimSpace(r.PrivateKeyPath) == "" {
		return "private_key_path is required"
	}
	if strings.TrimSpace(r.HostKeyFingerprint) != "" {
		if _, err := normalizeHostKeyFingerprint(r.HostKeyFingerprint); err != nil {
			return "host_key_fingerprint must be a SHA256 fingerprint, public key, or known_hosts line"
		}
	}
	return ""
}

func normalizeHostKeyFingerprint(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "SHA256:") {
		encoded := strings.TrimPrefix(value, "SHA256:")
		raw, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return "", err
		}
		if len(raw) != 32 {
			return "", errors.New("SHA256 fingerprint must contain 32 bytes")
		}
		return value, nil
	}
	if _, _, key, _, rest, err := ssh.ParseKnownHosts([]byte(value + "\n")); err == nil && len(strings.TrimSpace(string(rest))) == 0 {
		return ssh.FingerprintSHA256(key), nil
	}
	if key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(value + "\n")); err == nil && len(strings.TrimSpace(string(rest))) == 0 {
		return ssh.FingerprintSHA256(key), nil
	}
	return "", errors.New("unsupported host key format")
}

func requestTarget(req targetRequest) store.Target {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	fingerprint, _ := normalizeHostKeyFingerprint(req.HostKeyFingerprint)
	method := req.AuthMethod
	if method == "" {
		method = "key"
	}
	t := store.Target{Name: strings.TrimSpace(req.Name), Host: strings.TrimSpace(req.Host), Port: req.Port, SSHUser: strings.TrimSpace(req.SSHUser), AuthMethod: method, HostKeyFingerprint: fingerprint, Description: strings.TrimSpace(req.Description), Enabled: enabled}
	if method == "password" {
		t.Password = req.Password
	} else {
		t.PrivateKeyPath = strings.TrimSpace(req.PrivateKeyPath)
	}
	return t
}

func preserveTargetPassword(req *targetRequest, existing store.Target) {
	if req.AuthMethod == "password" && req.Password == "" && existing.AuthMethod == "password" {
		req.Password = existing.Password
	}
}
func (a *App) listTargets(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListTargets(r.Context())
	if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"items": items})
}

func (a *App) listAgentTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := a.store.ListTargets(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		if !target.Enabled {
			continue
		}
		if credentialID := credentialFromContext(r.Context()); credentialID != "" {
			allowed, err := a.store.CredentialCanAccessTarget(r.Context(), credentialID, target.ID)
			if err != nil {
				apiError(w, 500, "target authorization failed")
				return
			}
			if !allowed {
				continue
			}
		}
		items = append(items, map[string]any{"id": target.ID, "name": target.Name, "ssh_user": target.SSHUser, "description": target.Description})
	}
	jsonResponse(w, http.StatusOK, map[string]any{"items": items})
}
func (a *App) createTarget(w http.ResponseWriter, r *http.Request) {
	var req targetRequest
	if err := decode(w, r, &req); err != nil {
		apiError(w, 400, "invalid request")
		return
	}
	if msg := validateTarget(req); msg != "" {
		apiError(w, 400, msg)
		return
	}
	t := requestTarget(req)
	var err error
	t.ID, err = id.New("target")
	if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	if err = a.store.SaveTarget(r.Context(), t); err != nil {
		apiError(w, 500, err.Error())
		return
	}
	saved, _ := a.store.GetTarget(r.Context(), t.ID)
	jsonResponse(w, 201, saved)
}
func (a *App) updateTarget(w http.ResponseWriter, r *http.Request) {
	var req targetRequest
	if err := decode(w, r, &req); err != nil {
		apiError(w, 400, "invalid request")
		return
	}
	existing, err := a.store.GetTarget(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, 404, "target not found")
		return
	}
	if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	preserveTargetPassword(&req, existing)
	if msg := validateTarget(req); msg != "" {
		apiError(w, 400, msg)
		return
	}
	t := requestTarget(req)
	t.ID = r.PathValue("id")
	if err := a.store.UpdateTarget(r.Context(), t); errors.Is(err, store.ErrNotFound) {
		apiError(w, 404, "target not found")
		return
	} else if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	saved, _ := a.store.GetTarget(r.Context(), t.ID)
	jsonResponse(w, 200, saved)
}

func (a *App) deleteTarget(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteTarget(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "target not found")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) testTargetInput(w http.ResponseWriter, r *http.Request) {
	var req targetRequest
	if err := decode(w, r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.ExistingTargetID != "" {
		existing, err := a.store.GetTarget(r.Context(), req.ExistingTargetID)
		if errors.Is(err, store.ErrNotFound) {
			apiError(w, http.StatusNotFound, "target not found")
			return
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		preserveTargetPassword(&req, existing)
	}
	if message := validateTargetConnection(req); message != "" {
		apiError(w, http.StatusBadRequest, message)
		return
	}
	a.testConnection(w, r, requestTarget(req))
}

func (a *App) testSavedTarget(w http.ResponseWriter, r *http.Request) {
	target, err := a.store.GetTarget(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "target not found")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.testConnection(w, r, target)
}

func (a *App) testConnection(w http.ResponseWriter, r *http.Request, target store.Target) {
	started := time.Now()
	err := executor.TestConnection(r.Context(), target, 10*time.Second)
	durationMS := time.Since(started).Milliseconds()
	if err != nil {
		jsonResponse(w, http.StatusOK, map[string]any{"success": false, "duration_ms": durationMS, "message": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"success": true, "duration_ms": durationMS, "message": "SSH connection succeeded"})
}

func (a *App) listSessions(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListSessions(r.Context())
	if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"items": items})
}
func (a *App) getSession(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.GetSession(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, 404, "session not found")
		return
	} else if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	items, err := a.store.ListSessionExecutions(r.Context(), s.ID)
	if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	for i := range items {
		if err := a.attachArtifacts(r.Context(), &items[i]); err != nil {
			apiError(w, 500, err.Error())
			return
		}
	}
	jsonResponse(w, 200, map[string]any{"session": s, "executions": items})
}
func (a *App) listExecutions(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListExecutions(r.Context(), 100)
	if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	for i := range items {
		if err := a.attachArtifacts(r.Context(), &items[i]); err != nil {
			apiError(w, 500, err.Error())
			return
		}
	}
	jsonResponse(w, 200, map[string]any{"items": items})
}

func (a *App) createExecution(w http.ResponseWriter, r *http.Request) {
	req, uploads, cleanup, err := parseExecutionRequest(w, r)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	accepted, status, err := a.startExecution(r.Context(), req, uploads)
	if err != nil {
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, http.StatusAccepted, accepted)
}

func (a *App) startExecution(ctx context.Context, req executionRequest, uploads []uploadedFile) (acceptedExecution, int, error) {
	if req.TargetID == "" || strings.TrimSpace(req.Command) == "" {
		return acceptedExecution{}, http.StatusBadRequest, errors.New("target_id and command are required")
	}
	target, err := a.store.GetTarget(ctx, req.TargetID)
	if errors.Is(err, store.ErrNotFound) {
		return acceptedExecution{}, http.StatusNotFound, errors.New("target not found")
	}
	if err != nil {
		return acceptedExecution{}, http.StatusInternalServerError, err
	}
	if !target.Enabled {
		return acceptedExecution{}, http.StatusConflict, errors.New("target is disabled")
	}
	credentialID := credentialFromContext(ctx)
	if a.cfg.Mode == config.ModeServer {
		allowed, err := a.store.CredentialCanAccessTarget(ctx, credentialID, target.ID)
		if err != nil {
			return acceptedExecution{}, 500, err
		}
		if !allowed {
			return acceptedExecution{}, http.StatusForbidden, errors.New("target is not authorized")
		}
	}
	createSession := req.SessionID == ""
	sessionID := req.SessionID
	if createSession {
		sessionID, err = id.New("session")
		if err != nil {
			return acceptedExecution{}, http.StatusInternalServerError, err
		}
	}
	executionID, err := id.New("execution")
	if err != nil {
		return acceptedExecution{}, http.StatusInternalServerError, err
	}
	now := time.Now().UTC()
	e := store.Execution{ID: executionID, SessionID: sessionID, TargetID: req.TargetID, Title: strings.TrimSpace(req.Title), Command: req.Command, WorkingDir: req.WorkingDir, Status: "pending", CreatedAt: now}
	artifacts, artifactDir, err := a.archiveUploads(uploads, e)
	if err != nil {
		return acceptedExecution{}, http.StatusInternalServerError, err
	}
	session := store.AgentSession{ID: sessionID, Title: strings.TrimSpace(req.SessionTitle), AgentCredentialID: credentialID, CreatedAt: now, UpdatedAt: now}
	if session.Title == "" {
		session.Title = e.Title
	}
	if err = a.store.CreateExecution(ctx, session, e, artifacts, createSession); err != nil {
		if artifactDir != "" {
			_ = os.RemoveAll(artifactDir)
		}
		if errors.Is(err, store.ErrNotFound) {
			return acceptedExecution{}, http.StatusNotFound, errors.New("session not found")
		}
		return acceptedExecution{}, http.StatusInternalServerError, err
	}
	go a.runner.Run(context.Background(), e, target, artifacts)
	return acceptedExecution{SessionID: sessionID, ExecutionID: executionID, Status: "pending"}, http.StatusAccepted, nil
}

func parseExecutionRequest(w http.ResponseWriter, r *http.Request) (executionRequest, []uploadedFile, func(), error) {
	var req executionRequest
	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		if err := decode(w, r, &req); err != nil {
			return req, nil, nil, errors.New("invalid request")
		}
		if strings.Contains(req.Command, "{{file:") {
			return req, nil, nil, errors.New("command references files but request contains no uploads")
		}
		return req, nil, nil, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, 40<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return req, nil, nil, fmt.Errorf("parse multipart request: %w", err)
	}
	cleanup := func() { _ = r.MultipartForm.RemoveAll() }
	requestJSON := r.FormValue("request")
	decoder := json.NewDecoder(strings.NewReader(requestJSON))
	decoder.DisallowUnknownFields()
	if requestJSON == "" || decoder.Decode(&req) != nil {
		return req, nil, cleanup, errors.New("multipart field request must contain valid execution JSON")
	}
	if len(r.MultipartForm.File) > 8 {
		return req, nil, cleanup, errors.New("at most 8 files may be uploaded")
	}
	uploads := make([]uploadedFile, 0, len(r.MultipartForm.File))
	provided := make(map[string]bool)
	for placeholder, headers := range r.MultipartForm.File {
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(placeholder) {
			return req, nil, cleanup, fmt.Errorf("invalid file placeholder %q", placeholder)
		}
		if len(headers) != 1 {
			return req, nil, cleanup, fmt.Errorf("placeholder %q must have exactly one file", placeholder)
		}
		if headers[0].Size > 16<<20 {
			return req, nil, cleanup, fmt.Errorf("file %q exceeds 16 MiB limit", headers[0].Filename)
		}
		provided[placeholder] = true
		uploads = append(uploads, uploadedFile{Placeholder: placeholder, Header: headers[0]})
	}
	required := make(map[string]bool)
	for _, match := range placeholderPattern.FindAllStringSubmatch(req.Command, -1) {
		required[match[1]] = true
	}
	if strings.Contains(placeholderPattern.ReplaceAllString(req.Command, ""), "{{file:") {
		return req, nil, cleanup, errors.New("command contains an invalid file placeholder")
	}
	for placeholder := range required {
		if !provided[placeholder] {
			return req, nil, cleanup, fmt.Errorf("placeholder %q has no uploaded file", placeholder)
		}
	}
	for placeholder := range provided {
		if !required[placeholder] {
			return req, nil, cleanup, fmt.Errorf("uploaded file %q is not referenced by the command", placeholder)
		}
	}
	return req, uploads, cleanup, nil
}

func (a *App) archiveUploads(uploads []uploadedFile, execution store.Execution) ([]store.Artifact, string, error) {
	if len(uploads) == 0 {
		return nil, "", nil
	}
	dir := filepath.Join(a.cfg.DataDir, "artifacts", execution.CreatedAt.UTC().Format("2006/01/02"), execution.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create artifact directory: %w", err)
	}
	artifacts := make([]store.Artifact, 0, len(uploads))
	for _, upload := range uploads {
		artifactID, err := id.New("artifact")
		if err != nil {
			_ = os.RemoveAll(dir)
			return nil, "", err
		}
		var source io.ReadCloser
		filename := upload.Filename
		if upload.Header != nil {
			filename = upload.Header.Filename
			source, err = upload.Header.Open()
		} else {
			source = io.NopCloser(strings.NewReader(string(upload.Content)))
		}
		if err != nil {
			_ = os.RemoveAll(dir)
			return nil, "", fmt.Errorf("open upload %s: %w", filename, err)
		}
		localPath := filepath.Join(dir, artifactID)
		destination, err := os.OpenFile(localPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			source.Close()
			_ = os.RemoveAll(dir)
			return nil, "", fmt.Errorf("archive upload %s: %w", filename, err)
		}
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(source, (16<<20)+1))
		closeDestinationErr := destination.Close()
		closeSourceErr := source.Close()
		if copyErr != nil || closeDestinationErr != nil || closeSourceErr != nil || size > 16<<20 {
			_ = os.RemoveAll(dir)
			if size > 16<<20 {
				return nil, "", fmt.Errorf("file %q exceeds 16 MiB limit", filename)
			}
			return nil, "", fmt.Errorf("archive upload %s failed", filename)
		}
		artifacts = append(artifacts, store.Artifact{ID: artifactID, ExecutionID: execution.ID, Placeholder: upload.Placeholder, OriginalName: filepath.Base(filename), LocalPath: localPath, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), CreatedAt: execution.CreatedAt})
	}
	return artifacts, dir, nil
}
func (a *App) getExecution(w http.ResponseWriter, r *http.Request) {
	if !a.requireExecutionOwner(w, r) {
		return
	}
	e, err := a.store.GetExecution(r.Context(), r.PathValue("sessionID"), r.PathValue("executionID"))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, 404, "execution not found")
		return
	} else if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	if err := a.attachArtifacts(r.Context(), &e); err != nil {
		apiError(w, 500, err.Error())
		return
	}
	jsonResponse(w, 200, e)
}

func (a *App) attachArtifacts(ctx context.Context, execution *store.Execution) error {
	artifacts, err := a.store.ListArtifacts(ctx, execution.ID)
	if err != nil {
		return err
	}
	execution.Artifacts = artifacts
	return nil
}

func (a *App) downloadArtifact(w http.ResponseWriter, r *http.Request) {
	artifact, err := a.store.GetArtifact(r.Context(), r.PathValue("executionID"), r.PathValue("artifactID"))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	file, err := os.Open(artifact.LocalPath)
	if errors.Is(err, os.ErrNotExist) {
		apiError(w, http.StatusGone, "archived artifact file is missing")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifact.OriginalName}))
	http.ServeContent(w, r, artifact.OriginalName, artifact.CreatedAt, file)
}
func (a *App) getExecutionOutput(w http.ResponseWriter, r *http.Request) {
	if !a.requireExecutionOwner(w, r) {
		return
	}
	e, err := a.store.GetExecution(r.Context(), r.PathValue("sessionID"), r.PathValue("executionID"))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "execution not found")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.serveExecutionOutput(w, r, e)
}

func (a *App) getAdminExecutionOutput(w http.ResponseWriter, r *http.Request) {
	e, err := a.store.GetExecutionByID(r.Context(), r.PathValue("executionID"))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "execution not found")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.serveExecutionOutput(w, r, e)
}

func (a *App) serveExecutionOutput(w http.ResponseWriter, r *http.Request, e store.Execution) {
	if e.OutputPath == "" {
		apiError(w, http.StatusConflict, "output is not available yet")
		return
	}
	f, err := os.Open(e.OutputPath)
	if errors.Is(err, os.ErrNotExist) {
		apiError(w, http.StatusGone, "archived output file is missing")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="`+e.ID+`.output.jsonl"`)
	http.ServeContent(w, r, e.ID+".output.jsonl", e.CreatedAt, f)
}
func terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "timeout"
}
func (a *App) waitExecution(w http.ResponseWriter, r *http.Request) {
	if !a.requireExecutionOwner(w, r) {
		return
	}
	seconds := 55
	if raw := r.URL.Query().Get("timeout"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 60 {
			seconds = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(seconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		e, err := a.store.GetExecution(r.Context(), r.PathValue("sessionID"), r.PathValue("executionID"))
		if errors.Is(err, store.ErrNotFound) {
			apiError(w, 404, "execution not found")
			return
		} else if err != nil {
			apiError(w, 500, err.Error())
			return
		}
		if terminal(e.Status) {
			if err := a.attachArtifacts(r.Context(), &e); err != nil {
				apiError(w, 500, err.Error())
				return
			}
			jsonResponse(w, 200, e)
			return
		}
		select {
		case <-ctx.Done():
			if err := a.attachArtifacts(r.Context(), &e); err != nil {
				apiError(w, 500, err.Error())
				return
			}
			jsonResponse(w, 200, e)
			return
		case <-ticker.C:
		}
	}
}

func spaHandler() http.Handler {
	dist, err := fs.Sub(webui.Files, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(dist, path); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, "frontend is not built", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
