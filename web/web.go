// Package web contains the embedded browser interface.
package web

import "embed"

// Files contains only the public interface assets.
//
//go:embed index.html assets/app.css assets/app.js
var Files embed.FS
