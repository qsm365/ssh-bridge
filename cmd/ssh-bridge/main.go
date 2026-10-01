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
	mode := flag.String("mode", config.ModeLocal, "run mode: local or server")
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
		Mode:             *mode,
		Listen:           *listen,
		DataDir:          *dataDir,
		DatabaseURL:      os.Getenv("SSH_BRIDGE_DATABASE_URL"),
		AgentToken:       token,
		RevealAgentToken: generated,
		CommandTimeout:   10 * time.Minute,
	}
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	var data *store.Store
	if cfg.Mode == config.ModeServer {
		data, err = store.OpenServerDatabase(cfg.DatabaseName(), cfg.DatabaseURL)
	} else {
		data, err = store.Open(cfg.DatabasePath())
	}
	if err != nil {
		log.Fatal(err)
	}
	defer data.Close()
	if err := data.ConfigureSecrets(cfg.DataDir); err != nil {
		log.Fatal(err)
	}

	app := server.New(cfg, data)
	log.Printf("SSH Bridge %s mode is available at http://%s (%s)", cfg.Mode, *listen, cfg.DatabaseName())
	if generated {
		log.Printf("Agent Token has been generated; open http://%s/agent-access to copy it once", *listen)
	}
	if err := app.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
