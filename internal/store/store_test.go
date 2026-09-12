package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateExecutionCreatesAndReusesSession(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	target := Target{ID: "target_1", Name: "test", Host: "127.0.0.1", Port: 22, SSHUser: "root", PrivateKeyPath: "/missing", HostKeyFingerprint: "SHA256:test", Enabled: true}
	if err := s.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := AgentSession{ID: "session_1", Title: "诊断", CreatedAt: now, UpdatedAt: now}
	first := Execution{ID: "execution_1", SessionID: session.ID, TargetID: target.ID, Command: "uptime", Status: "pending", CreatedAt: now}
	if err := s.CreateExecution(ctx, session, first, nil, true); err != nil {
		t.Fatal(err)
	}
	second := Execution{ID: "execution_2", SessionID: session.ID, TargetID: target.ID, Command: "df -h", Status: "pending", CreatedAt: now.Add(time.Second)}
	if err := s.CreateExecution(ctx, session, second, nil, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExecutionCount != 2 {
		t.Fatalf("execution count = %d, want 2", got.ExecutionCount)
	}
}
