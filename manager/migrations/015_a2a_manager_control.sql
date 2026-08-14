CREATE TABLE IF NOT EXISTS task_a2a_rounds (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  execution_id TEXT NOT NULL,
  attempt INTEGER NOT NULL,
  turn INTEGER NOT NULL,
  operation TEXT NOT NULL,
  worker_id TEXT NOT NULL,
  command_id TEXT NOT NULL UNIQUE,
  parent_round_id TEXT,
  a2a_task_id TEXT,
  context_id TEXT,
  remote_status TEXT NOT NULL,
  last_sequence BIGINT NOT NULL DEFAULT 0,
  last_synced_at TIMESTAMP,
  last_network_at TIMESTAMP,
  unreachable_since TIMESTAMP,
  unknown_since TIMESTAMP,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  retryable BOOLEAN NOT NULL DEFAULT FALSE,
  version INTEGER NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  completed_at TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_task_a2a_round_attempt_turn
  ON task_a2a_rounds(task_id, attempt, turn);
CREATE INDEX IF NOT EXISTS idx_task_a2a_round_task_created
  ON task_a2a_rounds(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_task_a2a_round_reconcile
  ON task_a2a_rounds(completed_at, updated_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_a2a_round_remote_task
  ON task_a2a_rounds(worker_id, a2a_task_id);

CREATE TABLE IF NOT EXISTS a2a_dispatch_intents (
  id TEXT PRIMARY KEY,
  round_id TEXT NOT NULL REFERENCES task_a2a_rounds(id),
  task_id TEXT NOT NULL REFERENCES tasks(id),
  execution_id TEXT NOT NULL,
  worker_id TEXT NOT NULL,
  command_id TEXT NOT NULL UNIQUE,
  operation TEXT NOT NULL,
  status TEXT NOT NULL,
  payload TEXT NOT NULL DEFAULT '',
  attempt_count INTEGER NOT NULL DEFAULT 0,
  available_at TIMESTAMP NOT NULL,
  last_attempt_at TIMESTAMP,
  sent_at TIMESTAMP,
  completed_at TIMESTAMP,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_a2a_dispatch_due
  ON a2a_dispatch_intents(status, available_at, created_at);

CREATE TABLE IF NOT EXISTS a2a_event_inbox (
  round_id TEXT NOT NULL REFERENCES task_a2a_rounds(id),
  execution_id TEXT NOT NULL,
  event_id TEXT NOT NULL,
  payload_hash TEXT NOT NULL,
  event_sequence BIGINT NOT NULL,
  event_type TEXT NOT NULL,
  projection_status TEXT NOT NULL,
  projected_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (round_id, event_id),
  UNIQUE (execution_id, event_sequence)
);

CREATE INDEX IF NOT EXISTS idx_a2a_event_execution_sequence
  ON a2a_event_inbox(execution_id, event_sequence);
