package migrations

import "embed"

// FS embeds the SQL migration files for database initialization.
//go:embed *.sql
var FS embed.FS
