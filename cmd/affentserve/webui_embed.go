//go:build webui

package main

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:webui/dist
var webUIFS embed.FS

func webUIHandler() http.Handler {
	dist, err := fs.Sub(webUIFS, "webui/dist")
	if err != nil {
		return nil
	}
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeJSONErrorTyped(w, http.StatusMethodNotAllowed, "method not allowed", nil, "bad_request")
			return
		}
		if shouldServeIndex(dist, r.URL.Path) {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func shouldServeIndex(dist fs.FS, requestPath string) bool {
	clean := path.Clean("/" + strings.TrimPrefix(requestPath, "/"))
	if clean == "/" {
		return false
	}
	name := strings.TrimPrefix(clean, "/")
	if _, err := fs.Stat(dist, name); err == nil {
		return false
	}
	// Vite assets and obvious file requests should keep real 404s; route-like
	// paths fall back to index.html so refresh/deep-link works for the SPA.
	return path.Ext(name) == ""
}
