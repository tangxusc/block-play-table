package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

func (s *Service) SaveTaskGitBackup(ctx context.Context, backup domain.TaskGitBackup) error {
	if backup.ID == "" {
		backup.ID = "git_backup_" + uuid.NewString()
	}
	if backup.CreatedAt.IsZero() {
		backup.CreatedAt = s.clock()
	}
	return s.store.SaveTaskGitBackup(ctx, backup)
}
