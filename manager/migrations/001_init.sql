CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  git_url TEXT NOT NULL,
  default_branch TEXT NOT NULL,
  worktree_name_prefix TEXT NOT NULL,
  setup_commands TEXT NOT NULL DEFAULT '[]',
  archived BOOLEAN NOT NULL DEFAULT FALSE,
  version INTEGER NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  project_id TEXT NOT NULL REFERENCES projects(id),
  worker_id TEXT,
  agent_type TEXT NOT NULL,
  base_branch TEXT NOT NULL,
  target_branch TEXT NOT NULL,
  worktree_path TEXT,
  pre_commands TEXT NOT NULL DEFAULT '[]',
  post_commands TEXT NOT NULL DEFAULT '[]',
  result TEXT,
  version INTEGER NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS workers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  status TEXT NOT NULL,
  capabilities TEXT NOT NULL DEFAULT '{}',
  supported_agents TEXT NOT NULL DEFAULT '[]',
  work_dir TEXT NOT NULL,
  startup_command TEXT,
  project_binding_mode TEXT NOT NULL,
  current_task_id TEXT,
  last_heartbeat_at TIMESTAMP,
  version INTEGER NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS worker_project_bindings (
  worker_id TEXT NOT NULL REFERENCES workers(id),
  project_id TEXT NOT NULL REFERENCES projects(id),
  created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (worker_id, project_id)
);

CREATE TABLE IF NOT EXISTS system_agent_env_vars (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  description TEXT,
  enabled BOOLEAN NOT NULL,
  sensitive BOOLEAN NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS domain_events (
  id TEXT PRIMARY KEY,
  event_type TEXT NOT NULL,
  aggregate_type TEXT NOT NULL,
  aggregate_id TEXT NOT NULL,
  aggregate_version INTEGER NOT NULL,
  payload TEXT NOT NULL,
  occurred_at TIMESTAMP NOT NULL,
  correlation_id TEXT,
  causation_id TEXT
);

CREATE TABLE IF NOT EXISTS task_logs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  stream TEXT NOT NULL,
  content TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS task_conversations (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}',
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS processed_worker_messages (
  message_id TEXT PRIMARY KEY,
  processed_at TIMESTAMP NOT NULL
);
