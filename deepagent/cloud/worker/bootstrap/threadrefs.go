package bootstrap

import (
	cloudworker "eino-cli/deepagent/cloud/worker"
	"eino-cli/deepagent/cloud/worker/bootstrap/config"
	"eino-cli/deepagent/cloud/worker/bootstrap/internal/threadrefs"
	"gorm.io/gorm"
)

func newThreadRefStore(db *gorm.DB, cfg config.Config) cloudworker.ThreadRefStore {
	if !cfg.Features.ThreadRefs.Enabled {
		return nil
	}
	return threadrefs.New(db, cfg.Tables.ThreadRef)
}
