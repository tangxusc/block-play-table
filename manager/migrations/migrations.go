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

//go:embed 007_task_agent_config.sql
var TaskAgentConfigSQL string

//go:embed 008_worker_current_task_ids.sql
var WorkerCurrentTaskIDsSQL string

//go:embed 009_task_interactions.sql
var TaskInteractionsSQL string

//go:embed 010_unique_worker_name.sql
var UniqueWorkerNameSQL string

//go:embed 011_task_review.sql
var TaskReviewSQL string

//go:embed 012_drop_task_review.sql
var DropTaskReviewSQL string

//go:embed 013_task_desired_state.sql
var TaskDesiredStateSQL string

//go:embed 014_task_owner_user_id.sql
var TaskOwnerUserIDSQL string

// A2AManagerControlSQL 包含 Manager A2A 控制面表结构。
//
//go:embed 015_a2a_manager_control.sql
var A2AManagerControlSQL string

// DropLegacyTaskControlSQL 删除旧 WebSocket 任务控制状态。
//
//go:embed 016_drop_legacy_task_control.sql
var DropLegacyTaskControlSQL string

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
	{Version: "007_task_agent_config", SQL: TaskAgentConfigSQL},
	{Version: "008_worker_current_task_ids", SQL: WorkerCurrentTaskIDsSQL},
	{Version: "009_task_interactions", SQL: TaskInteractionsSQL},
	{Version: "010_unique_worker_name", SQL: UniqueWorkerNameSQL},
	{Version: "011_task_review", SQL: TaskReviewSQL},
	{Version: "012_drop_task_review", SQL: DropTaskReviewSQL},
	{Version: "013_task_desired_state", SQL: TaskDesiredStateSQL},
	{Version: "014_task_owner_user_id", SQL: TaskOwnerUserIDSQL},
	{Version: "015_a2a_manager_control", SQL: A2AManagerControlSQL},
	{Version: "016_drop_legacy_task_control", SQL: DropLegacyTaskControlSQL},
}
