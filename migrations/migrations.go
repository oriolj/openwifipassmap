// Package migrations embeds the SQLite schema so the server and CLI can apply
// it without a file dependency at runtime.
package migrations

import _ "embed"

//go:embed schema.sql
var Schema string
