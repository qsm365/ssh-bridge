package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"ssh-bridge/internal/config"
	"ssh-bridge/internal/id"
	"ssh-bridge/internal/store"
)

type credentialContextKey struct{}

func credentialFromContext(ctx context.Context) string {
	id, _ := ctx.Value(credentialContextKey{}).(string)
	return id
}

func agentTokenFromRequest(r *http.Request) string {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}
	return strings.TrimSpace(r.Header.Get("X-SSH-Bridge-Token"))
}

func (a *App) authenticateCredential(r *http.Request) (string, error) {
	token := agentTokenFromRequest(r)
	if token == "" {
		return "", nil
	}
	hash := sha256.Sum256([]byte(token))
	id, err := a.store.AuthenticateAgent(r.Context(), hex.EncodeToString(hash[:]))
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return id, err
}

func newCredentialToken() (string, string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", err
	}
	token := "sb_" + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, token[:12], hex.EncodeToString(hash[:]), nil
}

type credentialRequest struct {
	Name      string   `json:"name"`
	TargetIDs []string `json:"target_ids"`
	Enabled   *bool    `json:"enabled"`
}

func validateCredentialRequest(req credentialRequest) string {
	if strings.TrimSpace(req.Name) == "" {
		return "name is required"
	}
	seen := make(map[string]bool)
	for _, targetID := range req.TargetIDs {
		if targetID == "" || seen[targetID] {
			return "target_ids must be unique and non-empty"
		}
		seen[targetID] = true
	}
	return ""
}

func (a *App) listAgentCredentials(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Mode != config.ModeServer {
		apiError(w, 404, "server mode only")
		return
	}
	items, err := a.store.ListAgentCredentials(r.Context())
	if err != nil {
		apiError(w, 500, "list credentials failed")
		return
	}
	jsonResponse(w, 200, map[string]any{"items": items})
}

func (a *App) createAgentCredential(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Mode != config.ModeServer {
		apiError(w, 404, "server mode only")
		return
	}
	var req credentialRequest
	if decode(w, r, &req) != nil {
		apiError(w, 400, "invalid request")
		return
	}
	if msg := validateCredentialRequest(req); msg != "" {
		apiError(w, 400, msg)
		return
	}
	idValue, err := id.New("credential")
	if err != nil {
		apiError(w, 500, "create credential failed")
		return
	}
	token, prefix, hash, err := newCredentialToken()
	if err != nil {
		apiError(w, 500, "create credential failed")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	c := store.AgentCredential{ID: idValue, Name: strings.TrimSpace(req.Name), TokenPrefix: prefix, Enabled: enabled, TargetIDs: req.TargetIDs}
	if err := a.store.CreateAgentCredential(r.Context(), c, hash); errors.Is(err, store.ErrNotFound) {
		apiError(w, 400, "target not found")
		return
	} else if err != nil {
		apiError(w, 500, "create credential failed")
		return
	}
	jsonResponse(w, 201, map[string]any{"id": idValue, "token": token, "token_prefix": prefix, "name": c.Name, "enabled": c.Enabled, "target_ids": c.TargetIDs})
}

func (a *App) updateAgentCredential(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Mode != config.ModeServer {
		apiError(w, 404, "server mode only")
		return
	}
	var req credentialRequest
	if decode(w, r, &req) != nil {
		apiError(w, 400, "invalid request")
		return
	}
	if msg := validateCredentialRequest(req); msg != "" {
		apiError(w, 400, msg)
		return
	}
	if req.Enabled == nil {
		apiError(w, 400, "enabled is required")
		return
	}
	c := store.AgentCredential{ID: r.PathValue("id"), Name: strings.TrimSpace(req.Name), Enabled: *req.Enabled, TargetIDs: req.TargetIDs}
	if err := a.store.UpdateAgentCredential(r.Context(), c); errors.Is(err, store.ErrNotFound) {
		apiError(w, 404, "credential or target not found")
		return
	} else if err != nil {
		apiError(w, 500, "update credential failed")
		return
	}
	jsonResponse(w, 200, c)
}

func (a *App) regenerateAgentCredential(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Mode != config.ModeServer {
		apiError(w, 404, "server mode only")
		return
	}
	token, prefix, hash, err := newCredentialToken()
	if err != nil {
		apiError(w, 500, "rotate credential failed")
		return
	}
	if err := a.store.RotateAgentCredential(r.Context(), r.PathValue("id"), hash, prefix); errors.Is(err, store.ErrNotFound) {
		apiError(w, 404, "credential not found")
		return
	} else if err != nil {
		apiError(w, 500, "rotate credential failed")
		return
	}
	jsonResponse(w, 200, map[string]any{"token": token, "token_prefix": prefix})
}

func (a *App) requireExecutionOwner(w http.ResponseWriter, r *http.Request) bool {
	if a.cfg.Mode != config.ModeServer {
		return true
	}
	owned, err := a.store.SessionOwnedBy(r.Context(), r.PathValue("sessionID"), credentialFromContext(r.Context()))
	if err != nil {
		apiError(w, 500, "session lookup failed")
		return false
	}
	if !owned {
		apiError(w, 404, "execution not found")
		return false
	}
	return true
}
