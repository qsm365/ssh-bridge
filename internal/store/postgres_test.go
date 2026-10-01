package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPostgresMigrationsAndRestartRecovery(t *testing.T) {
	databaseURL := os.Getenv("SSH_BRIDGE_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("SSH_BRIDGE_TEST_POSTGRES_URL is not set")
	}

	testExternalDatabase(t, "postgres", databaseURL, OpenPostgres)
}

func TestMySQLMigrationsAndRestartRecovery(t *testing.T) {
	databaseURL := os.Getenv("SSH_BRIDGE_TEST_MYSQL_URL")
	if databaseURL == "" {
		t.Skip("SSH_BRIDGE_TEST_MYSQL_URL is not set")
	}
	testExternalDatabase(t, "mysql", databaseURL, OpenMySQL)
}

func testExternalDatabase(t *testing.T, databaseName, databaseURL string, opener func(string) (*Store, error)) {
	t.Helper()
	ctx := context.Background()
	s, err := opener(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var migrationVersion int
	if err := s.queryRow(ctx, `SELECT version FROM schema_migrations WHERE version=?`, 1).Scan(&migrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := s.queryRow(ctx, `SELECT version FROM schema_migrations WHERE version=?`, 4).Scan(&migrationVersion); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	target := Target{ID: "target_" + suffix, Name: databaseName + "-test", Host: "127.0.0.1", Port: 22, SSHUser: "root", PrivateKeyPath: "/missing", Enabled: true}
	if err := s.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	credential := AgentCredential{ID: "credential_" + suffix, Name: databaseName + " Agent", TokenPrefix: "sb_test", Enabled: true, TargetIDs: []string{target.ID}}
	if err := s.CreateAgentCredential(ctx, credential, "test_hash_"+suffix); err != nil {
		t.Fatal(err)
	}
	if allowed, err := s.CredentialCanAccessTarget(ctx, credential.ID, target.ID); err != nil || !allowed {
		t.Fatalf("credential target access = %v, %v", allowed, err)
	}
	session := AgentSession{ID: "session_" + suffix, Title: databaseName + " test", AgentCredentialID: credential.ID}
	execution := Execution{ID: "execution_" + suffix, SessionID: session.ID, TargetID: target.ID, Command: "uptime", Status: "pending", CreatedAt: time.Now().UTC()}
	if err := s.CreateExecution(ctx, session, execution, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRunning(ctx, execution.ID, "/tmp/output.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := opener(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetExecution(ctx, session.ID, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.FinishedAt == nil {
		t.Fatalf("recovered execution = %+v, want failed with finished_at", got)
	}
	if got.AgentCredentialID != credential.ID || got.AgentCredentialName != credential.Name {
		t.Fatalf("recovered attribution = %+v", got)
	}
}
