package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"ssh-bridge/internal/config"
	"ssh-bridge/internal/server"
	"ssh-bridge/internal/store"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:7408", "HTTP listen address")
	dataDir := flag.String("data-dir", "./ssh-bridge-data", "local data directory")
	flag.Parse()
	if err := os.MkdirAll(filepath.Clean(*dataDir), 0o700); err != nil {
		log.Fatal(err)
	}
	token, generated, err := config.LoadOrCreateAgentToken(*dataDir, os.Getenv("SSH_BRIDGE_AGENT_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}

	cfg := config.Config{
		Listen:           *listen,
		DataDir:          *dataDir,
		AgentToken:       token,
		RevealAgentToken: generated,
		CommandTimeout:   10 * time.Minute,
	}
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	data, err := store.Open(cfg.DatabasePath())
	if err != nil {
		log.Fatal(err)
	}
	defer data.Close()

	app := server.New(cfg, data)
	log.Printf("SSH Bridge is available at http://%s", *listen)
	if generated {
		log.Printf("Agent Token has been generated; open http://%s/agent-access to copy it once", *listen)
	}
	if err := app.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
