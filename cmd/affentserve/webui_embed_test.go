//go:build webui

package main

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestShouldServeIndex_FallsBackOnlyForSPARoutes(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":             &fstest.MapFile{Data: []byte("<div>app</div>")},
		"assets/app.abc123.js":   &fstest.MapFile{Data: []byte("console.log(1)")},
		"assets/app.abc123.css":  &fstest.MapFile{Data: []byte("body{}")},
		"favicon.svg":            &fstest.MapFile{Data: []byte("<svg />")},
		"nested/route/index.txt": &fstest.MapFile{Data: []byte("real file")},
	}

	cases := []struct {
		path string
		want bool
	}{
		{"/", false},
		{"/assets/app.abc123.js", false},
		{"/favicon.svg", false},
		{"/nested/route/index.txt", false},
		{"/sessions/abc", true},
		{"/settings", true},
		{"/missing.js", false},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := shouldServeIndex(dist, tc.path); got != tc.want {
				t.Fatalf("shouldServeIndex(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestEmbeddedWebUIFSHasIndex(t *testing.T) {
	dist, err := fs.Sub(webUIFS, "webui/dist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		t.Fatalf("embedded WebUI index.html missing: %v", err)
	}
}
