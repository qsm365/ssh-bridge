package webui

import "embed"

// Files contains the production frontend bundle.
//
//go:embed dist/*
var Files embed.FS
