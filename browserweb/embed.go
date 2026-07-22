// Package browserweb embeds the built browser-gateway reference SPA.
package browserweb

import "embed"

//go:embed all:dist
var StaticFS embed.FS
