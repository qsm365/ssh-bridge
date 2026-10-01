package config

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
)

const (
	ModeLocal          = "local"
	ModeServer         = "server"
	DatabaseSQLite     = "sqlite"
	DatabasePostgreSQL = "postgresql"
	DatabaseMySQL      = "mysql"
)

type Config struct {
	Mode             string
	Listen           string
	DataDir          string
	DatabaseURL      string
	AgentToken       string
	RevealAgentToken bool
	AdminPassword    string
	InsecureCookie   bool
	CommandTimeout   time.Duration
}

func (c Config) Validate() error {
	if c.Mode != ModeLocal && c.Mode != ModeServer {
		return fmt.Errorf("mode must be %q or %q", ModeLocal, ModeServer)
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("listen address must include host and port")
	}
	ip := net.ParseIP(host)
	if c.Mode == ModeLocal && !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return errors.New("local mode must listen on localhost or a loopback address")
	}
	if c.Mode == ModeServer && strings.TrimSpace(c.AdminPassword) == "" {
		return errors.New("SSH_BRIDGE_ADMIN_PASSWORD is required in server mode")
	}
	if c.Mode == ModeServer && strings.TrimSpace(c.DatabaseURL) == "" {
		return errors.New("SSH_BRIDGE_DATABASE_URL is required in server mode")
	}
	if c.Mode == ModeServer && c.DatabaseName() == "" {
		return errors.New("SSH_BRIDGE_DATABASE_URL must use postgres://, postgresql://, or mysql://")
	}
	if c.Mode == ModeLocal && c.AgentToken == "" {
		return errors.New("SSH_BRIDGE_AGENT_TOKEN is required")
	}
	if c.CommandTimeout <= 0 {
		return errors.New("command timeout must be positive")
	}
	return nil
}

func (c Config) DatabasePath() string { return filepath.Join(c.DataDir, "ssh-bridge.db") }
func (c Config) OutputDir() string    { return filepath.Join(c.DataDir, "outputs") }
func (c Config) DatabaseName() string {
	if c.Mode != ModeServer {
		return DatabaseSQLite
	}
	value := strings.ToLower(strings.TrimSpace(c.DatabaseURL))
	if strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://") {
		return DatabasePostgreSQL
	}
	if strings.HasPrefix(value, "mysql://") {
		return DatabaseMySQL
	}
	return ""
}
