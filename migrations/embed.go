// Package migrations embeds append-only SQL migration files.
package migrations

import "embed"

// FS holds every *.sql file in this directory, applied in lexical order by version name.
//
//go:embed *.sql
var FS embed.FS
