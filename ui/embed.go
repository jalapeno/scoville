// Package uiembed provides the embedded UI static assets for the syd binary.
// The ui/dist directory is produced by "npm run build" in the ui/ directory.
package uiembed

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
)

//go:embed dist/*
var assets embed.FS

// Handler returns an http.Handler that serves the embedded UI assets.
// All paths that don't match a static file are served index.html (SPA fallback).
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		panic("uiembed: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	// Pre-read index.html so we can serve it directly without going through
	// http.FileServer (which redirects /index.html → / causing a loop).
	indexData, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic("uiembed: index.html not found in dist/: " + err.Error())
	}

	serveIndex := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, string(indexData))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Root or /index.html → serve index directly (avoids FileServer redirect loop)
		if path == "/" || path == "/index.html" {
			serveIndex(w, r)
			return
		}

		// Try to serve the file directly. If it doesn't exist, serve index.html
		// (SPA client-side routing fallback).
		f, err := sub.Open(path[1:]) // strip leading /
		if err != nil {
			serveIndex(w, r)
			return
		}
		f.Close()
		fileServer.ServeHTTP(w, r)
	})
}
