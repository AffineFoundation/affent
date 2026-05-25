//go:build !webui

package main

import "net/http"

func webUIHandler() http.Handler {
	return nil
}
