CREATE TABLE IF NOT EXISTS task_git_turn_snapshots (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  before_tree TEXT NOT NULL,
  after_tree TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS task_review_runs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  scope TEXT NOT NULL,
  status TEXT NOT NULL,
  agent_type TEXT,
  summary TEXT NOT NULL DEFAULT '',
  raw_result TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMP,
  completed_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS task_review_findings (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES task_review_runs(id),
  task_id TEXT NOT NULL REFERENCES tasks(id),
  path TEXT NOT NULL,
  line INTEGER NOT NULL DEFAULT 0,
  severity TEXT NOT NULL,
  status TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  suggestion TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS task_review_comments (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  path TEXT NOT NULL,
  line INTEGER NOT NULL DEFAULT 0,
  body TEXT NOT NULL,
  resolved BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS task_git_backups (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  paths TEXT NOT NULL DEFAULT '[]',
  patch_path TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_task_git_turn_snapshots_task_created ON task_git_turn_snapshots(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_task_review_runs_task_created ON task_review_runs(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_task_review_findings_task_status ON task_review_findings(task_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_task_review_comments_task_created ON task_review_comments(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_task_git_backups_task_created ON task_git_backups(task_id, created_at);
