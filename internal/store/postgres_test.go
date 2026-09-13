package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPostgresMigrationsAndRestartRecovery(t *testing.T) {
	databaseURL := os.Getenv("SSH_BRIDGE_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("SSH_BRIDGE_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	s, err := OpenPostgres(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var migrationVersion int
	if err := s.queryRow(ctx, `SELECT version FROM schema_migrations WHERE version=?`, 1).Scan(&migrationVersion); err != nil {
		t.Fatal(err)
	}

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	target := Target{ID: "target_" + suffix, Name: "postgres-test", Host: "127.0.0.1", Port: 22, SSHUser: "root", PrivateKeyPath: "/missing", Enabled: true}
	if err := s.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	session := AgentSession{ID: "session_" + suffix, Title: "PostgreSQL test"}
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

	reopened, err := OpenPostgres(databaseURL)
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
}
