package web

import "embed"

// Files contains the Canvas static resources served by the backend.
//
//go:embed app.js index.html styles.css
var Files embed.FS
