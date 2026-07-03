// Package web embeds the frontend assets (SPA shell, CSS, JS, PWA files).
package web

import "embed"

// StaticFS holds all static frontend assets served by the application.
//
//go:embed all:static
var StaticFS embed.FS
