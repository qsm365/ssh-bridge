package store

import (
	"context"
	"path/filepath"
	"strings"
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

func TestMySQLURLConversion(t *testing.T) {
	dsn, err := mysqlDSN("mysql://bridge:p%40ss@db.example/ssh_bridge?charset=utf8mb4&tls=true")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"bridge:p@ss@tcp(db.example:3306)/ssh_bridge", "charset=utf8mb4", "tls=true"} {
		if !strings.Contains(dsn, expected) {
			t.Fatalf("DSN %q does not contain %q", dsn, expected)
		}
	}
	for _, invalid := range []string{"postgres://bridge@localhost/db", "mysql://localhost/db", "mysql://bridge@localhost"} {
		if _, err := mysqlDSN(invalid); err == nil {
			t.Fatalf("invalid MySQL URL %q was accepted", invalid)
		}
	}
}
