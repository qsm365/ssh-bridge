package config

import (
	"testing"
	"time"
)

func TestLocalModeRejectsNonLoopbackListener(t *testing.T) {
	base := Config{Mode: ModeLocal, Listen: "127.0.0.1:7408", AgentToken: "token", CommandTimeout: time.Minute}
	if err := base.Validate(); err != nil {
		t.Fatalf("loopback listener rejected: %v", err)
	}
	base.Listen = "0.0.0.0:7408"
	if err := base.Validate(); err == nil {
		t.Fatal("non-loopback listener was accepted")
	}
}

func TestServerModeRequiresDatabaseAndAdminPassword(t *testing.T) {
	base := Config{Mode: ModeServer, Listen: "127.0.0.1:7408", AgentToken: "token", AdminPassword: "test-password", CommandTimeout: time.Minute}
	if err := base.Validate(); err == nil {
		t.Fatal("server mode without database URL was accepted")
	}
	base.DatabaseURL = "postgres://bridge:secret@localhost/bridge"
	base.AdminPassword = ""
	if err := base.Validate(); err == nil {
		t.Fatal("server mode without admin password was accepted")
	}
	base.AdminPassword = "test-password"
	if err := base.Validate(); err != nil {
		t.Fatalf("valid server config rejected: %v", err)
	}
	if got := base.DatabaseName(); got != DatabasePostgreSQL {
		t.Fatalf("PostgreSQL database name = %q", got)
	}
	base.DatabaseURL = "mysql://bridge:secret@localhost/bridge"
	if err := base.Validate(); err != nil {
		t.Fatalf("valid MySQL server config rejected: %v", err)
	}
	if got := base.DatabaseName(); got != DatabaseMySQL {
		t.Fatalf("MySQL database name = %q", got)
	}
	base.DatabaseURL = "sqlite:///tmp/bridge.db"
	if err := base.Validate(); err == nil {
		t.Fatal("unsupported server database was accepted")
	}
	base.DatabaseURL = "postgres://bridge:secret@localhost/bridge"
	base.Listen = "0.0.0.0:7408"
	if err := base.Validate(); err != nil {
		t.Fatalf("authenticated server listener rejected: %v", err)
	}
}
