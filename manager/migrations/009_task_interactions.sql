CREATE TABLE IF NOT EXISTS task_interactions (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  raw_payload TEXT NOT NULL DEFAULT '',
  agent_session_id TEXT,
  response_decision TEXT,
  response_message TEXT,
  response_payload TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_task_interactions_task_status ON task_interactions(task_id, status, created_at);
