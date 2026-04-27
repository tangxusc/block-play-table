ALTER TABLE workers ADD COLUMN IF NOT EXISTS current_task_ids TEXT NOT NULL DEFAULT '[]';

UPDATE workers
SET current_task_ids = CASE
  WHEN current_task_id IS NULL OR TRIM(current_task_id) = '' THEN '[]'
  WHEN EXISTS (
    SELECT 1
    FROM tasks
    WHERE tasks.id = workers.current_task_id
      AND tasks.status IN ('STARTING', 'RUNNING', 'WAITING_INPUT', 'INTERRUPTING')
  ) THEN '["' || current_task_id || '"]'
  ELSE '[]'
END;

ALTER TABLE workers DROP COLUMN IF EXISTS current_task_id;
