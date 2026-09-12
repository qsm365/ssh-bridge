package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const agentTokenFilename = "agent-token"

func AgentTokenPath(dataDir string) string {
	return filepath.Join(dataDir, agentTokenFilename)
}

func LoadOrCreateAgentToken(dataDir, bootstrap string) (string, bool, error) {
	path := AgentTokenPath(dataDir)
	content, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(content))
		if token == "" {
			return "", false, errors.New("agent token file is empty")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", false, fmt.Errorf("secure agent token file: %w", err)
		}
		return token, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("read agent token: %w", err)
	}

	token := strings.TrimSpace(bootstrap)
	generated := token == ""
	if generated {
		token, err = GenerateAgentToken()
		if err != nil {
			return "", false, err
		}
	}
	if err := writeAgentToken(path, token, true); err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadOrCreateAgentToken(dataDir, bootstrap)
		}
		return "", false, err
	}
	return token, generated, nil
}

func ReplaceAgentToken(dataDir string) (string, error) {
	token, err := GenerateAgentToken()
	if err != nil {
		return "", err
	}
	path := AgentTokenPath(dataDir)
	temporary, err := os.CreateTemp(dataDir, ".agent-token-*")
	if err != nil {
		return "", fmt.Errorf("create temporary agent token: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", fmt.Errorf("secure temporary agent token: %w", err)
	}
	if _, err := temporary.WriteString(token + "\n"); err != nil {
		temporary.Close()
		return "", fmt.Errorf("write temporary agent token: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close temporary agent token: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", fmt.Errorf("replace agent token: %w", err)
	}
	return token, nil
}

func GenerateAgentToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate agent token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func writeAgentToken(path, token string, exclusive bool) error {
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if exclusive {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		file.Close()
		return fmt.Errorf("write agent token: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close agent token: %w", err)
	}
	return nil
}
