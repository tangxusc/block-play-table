DROP TABLE IF EXISTS processed_worker_messages;
ALTER TABLE tasks DROP COLUMN IF EXISTS pending_directive;
