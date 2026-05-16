-- +migrate: add owner_user_id column to tasks
ALTER TABLE tasks ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';
