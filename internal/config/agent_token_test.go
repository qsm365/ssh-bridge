package config

import (
	"os"
	"testing"
)

func TestAgentTokenLifecycle(t *testing.T) {
	dir := t.TempDir()
	token, generated, err := LoadOrCreateAgentToken(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !generated || len(token) < 40 {
		t.Fatalf("unexpected generated token: generated=%v length=%d", generated, len(token))
	}
	info, err := os.Stat(AgentTokenPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token permissions = %o", info.Mode().Perm())
	}
	reloaded, generated, err := LoadOrCreateAgentToken(dir, "ignored-bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if generated || reloaded != token {
		t.Fatalf("token was not reused: generated=%v", generated)
	}
	replaced, err := ReplaceAgentToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if replaced == token {
		t.Fatal("replacement token did not change")
	}
	persisted, _, err := LoadOrCreateAgentToken(dir, "")
	if err != nil || persisted != replaced {
		t.Fatalf("replacement token was not persisted: %v", err)
	}
}

func TestAgentTokenUsesBootstrapOnlyForFirstCreation(t *testing.T) {
	dir := t.TempDir()
	token, generated, err := LoadOrCreateAgentToken(dir, "bootstrap-token")
	if err != nil {
		t.Fatal(err)
	}
	if generated || token != "bootstrap-token" {
		t.Fatalf("bootstrap result = %q, generated=%v", token, generated)
	}
}
