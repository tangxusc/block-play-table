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

CREATE INDEX IF NOT EXISTS idx_worker_agent_env_vars_worker_agent ON worker_agent_env_vars(worker_id, agent_type);

DROP TABLE IF EXISTS system_agent_env_vars;
