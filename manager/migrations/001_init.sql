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

CREATE TABLE IF NOT EXISTS worker_agent_env_vars (
  worker_id TEXT NOT NULL REFERENCES workers(id),
  agent_type TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  description TEXT,
  enabled BOOLEAN NOT NULL,
  sensitive BOOLEAN NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (worker_id, agent_type, key)
);

CREATE TABLE IF NOT EXISTS system_settings (
  id TEXT PRIMARY KEY,
  worker_heartbeat_timeout TEXT NOT NULL,
  security_policy TEXT NOT NULL,
  version INTEGER NOT NULL,
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

CREATE TABLE IF NOT EXISTS outbox_messages (
  id TEXT PRIMARY KEY,
  event_id TEXT NOT NULL REFERENCES domain_events(id),
  event_type TEXT NOT NULL,
  payload TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL,
  published_at TIMESTAMP
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

CREATE INDEX IF NOT EXISTS idx_tasks_created_at ON tasks(created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_worker_id ON tasks(worker_id);
CREATE INDEX IF NOT EXISTS idx_workers_status ON workers(status);
CREATE INDEX IF NOT EXISTS idx_worker_project_bindings_project_id ON worker_project_bindings(project_id);
CREATE INDEX IF NOT EXISTS idx_worker_agent_env_vars_worker_agent ON worker_agent_env_vars(worker_id, agent_type);
CREATE INDEX IF NOT EXISTS idx_domain_events_aggregate ON domain_events(aggregate_type, aggregate_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_domain_events_type ON domain_events(event_type, occurred_at);
CREATE INDEX IF NOT EXISTS idx_outbox_messages_status ON outbox_messages(status, created_at);
CREATE INDEX IF NOT EXISTS idx_task_logs_task_id ON task_logs(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_task_conversations_task_id ON task_conversations(task_id, created_at);
