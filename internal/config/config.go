package config

import (
	"errors"
	"net"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Listen           string
	DataDir          string
	AgentToken       string
	RevealAgentToken bool
	CommandTimeout   time.Duration
}

func (c Config) Validate() error {
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("listen address must include host and port")
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return errors.New("local mode must listen on localhost or a loopback address")
	}
	if c.AgentToken == "" {
		return errors.New("SSH_BRIDGE_AGENT_TOKEN is required")
	}
	if c.CommandTimeout <= 0 {
		return errors.New("command timeout must be positive")
	}
	return nil
}

func (c Config) DatabasePath() string { return filepath.Join(c.DataDir, "ssh-bridge.db") }
func (c Config) OutputDir() string    { return filepath.Join(c.DataDir, "outputs") }
