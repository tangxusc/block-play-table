ALTER TABLE tasks ADD COLUMN IF NOT EXISTS agent_config TEXT NOT NULL DEFAULT '{}';

UPDATE tasks SET agent_config = '{}' WHERE agent_config IS NULL OR TRIM(agent_config) = '';
