package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeletedTargetIsHiddenButExecutionHistoryRemains(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	target := Target{ID: "target_deleted", Name: "historical host", Host: "127.0.0.1", Port: 22, SSHUser: "operator", PrivateKeyPath: "/missing", Enabled: true}
	if err := s.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	session := AgentSession{ID: "session_deleted"}
	execution := Execution{ID: "execution_deleted", SessionID: session.ID, TargetID: target.ID, Command: "uptime", Status: "failed", CreatedAt: time.Now().UTC()}
	if err := s.CreateExecution(ctx, session, execution, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTarget(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListTargets(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted target is still listed: %v, %v", items, err)
	}
	if _, err := s.GetTarget(ctx, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted target is still accessible: %v", err)
	}
	if err := s.UpdateTarget(ctx, target); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted target can be edited: %v", err)
	}
	if err := s.DeleteTarget(ctx, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete should fail: %v", err)
	}
	got, err := s.GetExecution(ctx, session.ID, execution.ID)
	if err != nil || got.TargetName != target.Name {
		t.Fatalf("execution lost its host: %+v, %v", got, err)
	}
	var deletedAt string
	if err := s.db.QueryRow(`SELECT deleted_at FROM targets WHERE id=?`, target.ID).Scan(&deletedAt); err != nil || deletedAt == "" {
		t.Fatalf("target row was removed: %v", err)
	}
}

func TestPasswordTargetEncryptedAndNotSerialized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureSecrets(dir); err != nil {
		t.Fatal(err)
	}
	target := Target{ID: "target_password", Name: "test", Host: "127.0.0.1", Port: 22, SSHUser: "operator", AuthMethod: "password", Password: "example-secret-123", Enabled: true}
	if err := s.SaveTarget(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	var ciphertext string
	if err := s.db.QueryRow(`SELECT password_ciphertext FROM targets WHERE id=?`, target.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if ciphertext == target.Password || strings.Contains(ciphertext, target.Password) {
		t.Fatal("password stored as plaintext")
	}
	got, err := s.GetTarget(context.Background(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != target.Password || !got.PasswordConfigured {
		t.Fatal("saved password could not be read")
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), target.Password) {
		t.Fatal("password leaked in target JSON")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ConfigureSecrets(dir); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetTarget(context.Background(), target.ID)
	if err != nil || got.Password != target.Password {
		t.Fatalf("password lost after reopen: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "target-password.key")); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureSecrets(dir); err == nil {
		t.Fatal("missing encryption key was silently replaced")
	}
}

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
