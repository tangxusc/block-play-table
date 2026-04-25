package migrations

import _ "embed"

//go:embed 001_init.sql
var SchemaSQL string

type Migration struct {
	Version string
	SQL     string
}

var All = []Migration{
	{Version: "001_init", SQL: SchemaSQL},
}
