package migrations

import _ "embed"

//go:embed 001_init.sql
var SchemaSQL string

//go:embed 002_drop_project_setup_commands.sql
var DropProjectSetupCommandsSQL string

//go:embed 003_drop_task_target_branch.sql
var DropTaskBranchSQL string

type Migration struct {
	Version string
	SQL     string
}

var All = []Migration{
	{Version: "001_init", SQL: SchemaSQL},
	{Version: "002_drop_project_setup_commands", SQL: DropProjectSetupCommandsSQL},
	{Version: "003_drop_task_target_branch", SQL: DropTaskBranchSQL},
}
