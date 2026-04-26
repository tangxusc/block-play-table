ALTER TABLE tasks ADD COLUMN IF NOT EXISTS start_date TIMESTAMP;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS end_date TIMESTAMP;

UPDATE tasks SET start_date = created_at WHERE start_date IS NULL;
UPDATE tasks SET end_date = created_at WHERE end_date IS NULL;
