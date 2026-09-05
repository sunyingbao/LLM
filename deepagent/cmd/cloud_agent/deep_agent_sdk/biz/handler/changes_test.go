package handler

import (
	"encoding/json"
	"strings"
	"testing"

	changesvc "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/changes"
	servicecommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
)

func TestChangesResponseKeepsTheExistingBaseRespContract(t *testing.T) {
	body, err := json.Marshal(&listChangesResponse{
		Changes:  []changesvc.ChangeInfo{{Path: "main.go", Status: "modified"}},
		BaseResp: servicecommon.BaseRespOK(),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, expected := range []string{`"changes"`, `"path":"main.go"`, `"BaseResp"`, `"StatusCode":0`} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("response = %s, missing %s", body, expected)
		}
	}
}
