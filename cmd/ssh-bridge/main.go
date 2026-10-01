package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"ssh-bridge/internal/config"
	"ssh-bridge/internal/server"
	"ssh-bridge/internal/store"
)

func main() {
	checkReady := flag.Bool("check-ready", false, "check the local service readiness")
	mode := flag.String("mode", config.ModeLocal, "run mode: local or server")
	listen := flag.String("listen", "127.0.0.1:7408", "HTTP listen address")
	dataDir := flag.String("data-dir", "./ssh-bridge-data", "local data directory")
	flag.Parse()
	if *checkReady {
		client := http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://127.0.0.1:7408/readyz")
		if err != nil {
			log.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			log.Fatalf("readyz returned HTTP %d", response.StatusCode)
		}
		return
	}
	if err := os.MkdirAll(filepath.Clean(*dataDir), 0o700); err != nil {
		log.Fatal(err)
	}
	var token string
	var generated bool
	var err error
	if *mode == config.ModeLocal {
		token, generated, err = config.LoadOrCreateAgentToken(*dataDir, os.Getenv("SSH_BRIDGE_AGENT_TOKEN"))
		if err != nil {
			log.Fatal(err)
		}
	}
	cookieSecure := true
	if raw, set := os.LookupEnv("SSH_BRIDGE_COOKIE_SECURE"); set {
		cookieSecure, err = strconv.ParseBool(raw)
		if err != nil {
			log.Fatal("SSH_BRIDGE_COOKIE_SECURE must be true or false")
		}
	}

	cfg := config.Config{
		Mode:             *mode,
		Listen:           *listen,
		DataDir:          *dataDir,
		DatabaseURL:      os.Getenv("SSH_BRIDGE_DATABASE_URL"),
		AgentToken:       token,
		RevealAgentToken: generated,
		AdminPassword:    os.Getenv("SSH_BRIDGE_ADMIN_PASSWORD"),
		InsecureCookie:   !cookieSecure,
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
