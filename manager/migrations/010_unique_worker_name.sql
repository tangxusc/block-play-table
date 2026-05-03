WITH duplicate_workers AS (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY name ORDER BY created_at, id) AS duplicate_rank
  FROM workers
)
UPDATE workers
SET name = name || ' (' || id || ')'
WHERE id IN (
  SELECT id
  FROM duplicate_workers
  WHERE duplicate_rank > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_workers_name_unique ON workers(name);
