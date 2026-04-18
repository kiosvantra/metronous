package web

import "embed"

//go:embed static/index.html
var staticFS embed.FS
