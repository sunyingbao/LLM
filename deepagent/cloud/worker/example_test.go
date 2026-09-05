//go:build !windows

package worker_test

func Example() {
	// In a real service, construct the chat model and stores from service config.
	//
	// cfg := worker.Config{
	//     Host: worker.HostConfig{
	//         Namespace:   "deep_agent_sdk",
	//         Concurrency: 4,
	//     },
	//     Thread: worker.ThreadConfig{
	//         WorkDir:   "/data/agent_workdir",
	//     },
	//     Turn: worker.TurnConfig{
	//         Defaults: worker.TurnDefaults{
	//             Capabilities: worker.TurnCapabilities{
	//                 Skills: worker.SkillConfig{Sources: []string{"/data/agent_skills"}},
	//             },
	//         },
	//         Models: map[string]worker.ModelProfile{
	//             "main_model": {ChatModel: chatModel, ModelName: "doubao"},
	//         },
	//         Roles: map[string]worker.RolePreset{
	//             worker.DefaultRoleID: {
	//                 Model:          worker.ModelPolicy{Default: "main_model"},
	//                 ApprovalPolicy: worker.ApprovalPolicyNormal,
	//             },
	//         },
	//     },
	// }
	//
	// deps := worker.Deps{
	//     CoordinatorClient: worker.NewCoordinatorClient(coordinator),
	//     HistoryStore:      historyProvider,
	//     CheckpointStore:   checkpointProvider,
	//     Approvals:         worker.NewApprovalStore(),
	//     MessageWaitObserver: worker.TaskMessageWaitObserver,
	// }
	//
	// return worker.Run(ctx, cfg, deps)
}
