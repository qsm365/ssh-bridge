package config

import (
	"testing"
	"time"
)

func TestLocalModeRejectsNonLoopbackListener(t *testing.T) {
	base := Config{Listen: "127.0.0.1:7408", AgentToken: "token", CommandTimeout: time.Minute}
	if err := base.Validate(); err != nil {
		t.Fatalf("loopback listener rejected: %v", err)
	}
	base.Listen = "0.0.0.0:7408"
	if err := base.Validate(); err == nil {
		t.Fatal("non-loopback listener was accepted")
	}
}
