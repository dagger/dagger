package server

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

//go:embed web/*
var embeddedWebFS embed.FS

var webFS fs.FS

func init() {
	sub, err := fs.Sub(embeddedWebFS, "web")
	if err != nil {
		panic(err)
	}
	webFS = sub
}

func serveWebAsset(w http.ResponseWriter, r *http.Request) {
	cleanPath := path.Clean("/" + r.URL.Path)
	assetPath := strings.TrimPrefix(cleanPath, "/")
	if assetPath == "" || assetPath == "." {
		assetPath = "index.html"
	}

	data, err := fs.ReadFile(webFS, assetPath)
	if err != nil {
		// Single-page app fallback.
		data, err = fs.ReadFile(webFS, "index.html")
		if err != nil {
			http.Error(w, "web ui unavailable", http.StatusInternalServerError)
			return
		}
		assetPath = "index.html"
	}

	if contentType := mime.TypeByExtension(filepath.Ext(assetPath)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if assetPath == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
