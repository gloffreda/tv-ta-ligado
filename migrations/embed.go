// Package migrations embute os arquivos SQL versionados.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
