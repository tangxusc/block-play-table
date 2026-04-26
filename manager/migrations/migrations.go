package migrations

import _ "embed"

//go:embed 001_init.sql
var SchemaSQL string

//go:embed 002_drop_project_setup_commands.sql
var DropProjectSetupCommandsSQL string

//go:embed 003_drop_task_target_branch.sql
var DropTaskBranchSQL string

//go:embed 004_worker_agent_runtime_env.sql
var WorkerAgentRuntimeEnvSQL string

//go:embed 005_task_agent_session.sql
var TaskAgentSessionSQL string

//go:embed 006_task_display_dates.sql
var TaskDisplayDatesSQL string

type Migration struct {
	Version string
	SQL     string
}

var All = []Migration{
	{Version: "001_init", SQL: SchemaSQL},
	{Version: "002_drop_project_setup_commands", SQL: DropProjectSetupCommandsSQL},
	{Version: "003_drop_task_target_branch", SQL: DropTaskBranchSQL},
	{Version: "004_worker_agent_runtime_env", SQL: WorkerAgentRuntimeEnvSQL},
	{Version: "005_task_agent_session", SQL: TaskAgentSessionSQL},
	{Version: "006_task_display_dates", SQL: TaskDisplayDatesSQL},
}
