package main

import (
	"context"
	"log"
	"time"

	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/config"
	sessiondal "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/dal/session"
	sessionac "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/infra/ac"
	sessionidgen "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/infra/idgen"
	sessionmysql "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/infra/mysql"
	aic_agent_sdk_session "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/aic_agent_sdk_session/aicagentsdksessionservice"
	sessionservice "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/service/session"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db, err := sessionmysql.Open(ctx, sessionmysql.Config{
		PSM:         cfg.MySQL.PSM,
		DBName:      cfg.MySQL.DBName,
		DSN:         cfg.MySQL.DSN,
		ReadTimeout: time.Duration(cfg.MySQL.ReadTimeoutMS) * time.Millisecond,
	})
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close mysql: %v", err)
		}
	}()

	store, err := sessiondal.NewStore(db, cfg.AgentSessionTable)
	if err != nil {
		log.Fatalf("create session store: %v", err)
	}

	var acClient sessionservice.CoordinatorClient
	if !cfg.DisableAC {
		acClient, err = sessionac.NewClient(cfg.CoordinatorPSM, cfg.CoordinatorCluster, config.ParseHostPorts(cfg.CoordinatorHostports), cfg.CoordinatorNamespace)
		if err != nil {
			log.Fatalf("create ac client: %v", err)
		}
	} else {
		log.Printf("agent_coordinator client disabled; include_threads returns empty threads and CloseSession only updates t_agent_session")
	}

	idGenerator, err := sessionidgen.NewGenerator(cfg.IDGen.Namespace)
	if err != nil {
		log.Fatalf("create id generator: %v", err)
	}
	svc, err := sessionservice.New(store, idGenerator, acClient)
	if err != nil {
		log.Fatalf("create session service: %v", err)
	}

	svr := aic_agent_sdk_session.NewServer(NewAICAgentSDKSessionServiceImpl(svc))
	if err := svr.Run(); err != nil {
		log.Println(err.Error())
	}
}
