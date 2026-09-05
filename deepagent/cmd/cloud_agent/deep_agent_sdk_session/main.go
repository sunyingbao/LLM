package sessionapp

import (
	"context"
	"fmt"
	"time"

	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/config"
	sessiondal "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/dal/session"
	sessionac "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/infra/ac"
	sessionidgen "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/infra/idgen"
	sessionmysql "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/infra/mysql"
	sessionservice "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/service/session"
	"eino-cli/deepagent/coordinator"
)

func New(ctx context.Context, cfg config.Config, core *coordinator.Coordinator) (service *sessionservice.Service, closeFunc func() error, err error) {
	db, err := sessionmysql.Open(ctx, sessionmysql.Config{
		DSN:         cfg.MySQL.DSN,
		ReadTimeout: time.Duration(cfg.MySQL.ReadTimeoutMS) * time.Millisecond,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("open session mysql: %w", err)
	}
	closeFunc = db.Close

	store, err := sessiondal.NewStore(db, cfg.AgentSessionTable)
	if err != nil {
		_ = closeFunc()
		return nil, nil, fmt.Errorf("create session store: %w", err)
	}
	coordinatorClient, err := sessionac.NewClient(core, cfg.CoordinatorNamespace)
	if err != nil {
		_ = closeFunc()
		return nil, nil, err
	}
	idGenerator, err := sessionidgen.NewGenerator(cfg.IDGen.Namespace)
	if err != nil {
		_ = closeFunc()
		return nil, nil, fmt.Errorf("create id generator: %w", err)
	}
	sessionService, err := sessionservice.New(store, idGenerator, coordinatorClient)
	if err != nil {
		_ = closeFunc()
		return nil, nil, err
	}
	return sessionService, closeFunc, nil
}
