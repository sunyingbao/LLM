package passport

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"code.byted.org/middleware/hertz/pkg/app"
	sso "code.byted.org/sys/sso-jwt-parser-sdk-go"
	ssojwt "code.byted.org/sys/sso-jwt-parser-sdk-go/jwt"
)

type HertzContext = app.RequestContext

const bytedanceUserHeader = "X-Bytedance-User"
const boeTestUIDHeader = "X-AIC-Agent-SDK-Test-UID"

type Identity struct {
	UserID         int64
	UserName       string
	Email          string
	EmployeeNumber int64
}

var ssoParser ssojwt.Parser = sso.New([]string{ssojwt.RegionBOE, ssojwt.RegionCN})

func GetUserID(hertzCtx *HertzContext) (int64, error) {
	identity, err := GetIdentity(hertzCtx)
	if err != nil {
		return 0, err
	}
	return identity.UserID, nil
}

func GetIdentity(hertzCtx *HertzContext) (Identity, error) {
	if fakePassportEnabled() {
		uid := fakeUID()
		return Identity{UserID: uid, EmployeeNumber: uid, UserName: "local"}, nil
	}
	if hertzCtx == nil {
		return Identity{}, fmt.Errorf("passport request context is required")
	}
	if strings.TrimSpace(string(hertzCtx.Request.Header.Peek(boeTestUIDHeader))) == "1234" {
		return Identity{UserID: 1234, EmployeeNumber: 1234, UserName: "codex", Email: "codex@boe.local"}, nil
	}
	token := strings.TrimSpace(string(hertzCtx.Request.Header.Peek(bytedanceUserHeader)))
	if token == "" {
		return Identity{}, fmt.Errorf("missing %s", bytedanceUserHeader)
	}
	payload, err := ssoParser.Validate(context.Background(), token)
	if err != nil {
		return Identity{}, fmt.Errorf("validate %s: %w", bytedanceUserHeader, err)
	}
	if payload == nil {
		return Identity{}, fmt.Errorf("empty %s payload", bytedanceUserHeader)
	}
	if payload.EmployeeNumber <= 0 {
		return Identity{}, fmt.Errorf("missing employee_number in %s payload", bytedanceUserHeader)
	}
	return Identity{
		UserID:         payload.EmployeeNumber,
		UserName:       strings.TrimSpace(payload.UserName),
		Email:          strings.TrimSpace(payload.Email),
		EmployeeNumber: payload.EmployeeNumber,
	}, nil
}

func fakePassportEnabled() bool {
	value := strings.TrimSpace(os.Getenv("AIC_AGENT_SDK_API_FAKE_PASSPORT"))
	return value == "1" || strings.EqualFold(value, "true")
}

func fakeUID() int64 {
	raw := strings.TrimSpace(os.Getenv("AIC_AGENT_SDK_API_FAKE_UID"))
	if raw == "" {
		return 1234
	}
	uid, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || uid <= 0 {
		return 1234
	}
	return uid
}
