package passport

import (
	"context"
	"testing"

	"code.byted.org/middleware/hertz/pkg/app"
	ssojwt "code.byted.org/sys/sso-jwt-parser-sdk-go/jwt"
)

type fakeParser struct {
	payload *ssojwt.Payload
	err     error
}

func (f fakeParser) Validate(ctx context.Context, jwtStr string, hosts ...string) (*ssojwt.Payload, error) {
	return f.payload, f.err
}

func (f fakeParser) ValidateSignatureOnly(ctx context.Context, jwtStr string, hosts ...string) (*ssojwt.Payload, error) {
	return f.payload, f.err
}

func TestGetIdentityUsesEmployeeNumberAsUID(t *testing.T) {
	oldParser := ssoParser
	t.Cleanup(func() { ssoParser = oldParser })
	ssoParser = fakeParser{payload: &ssojwt.Payload{
		UserName:       "alice",
		Email:          "alice@example.com",
		UserID:         "999999",
		EmployeeNumber: 123456,
	}}

	var c app.RequestContext
	c.Request.Header.Set(bytedanceUserHeader, "jwt-token")

	identity, err := GetIdentity(&c)
	if err != nil {
		t.Fatalf("GetIdentity() error = %v", err)
	}
	if identity.UserID != 123456 {
		t.Fatalf("identity.UserID=%d, want employee number 123456", identity.UserID)
	}
	if identity.UserName != "alice" || identity.Email != "alice@example.com" || identity.EmployeeNumber != 123456 {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestGetIdentityUsesBOETestHeader(t *testing.T) {
	var c app.RequestContext
	c.Request.Header.Set(boeTestUIDHeader, "1234")

	identity, err := GetIdentity(&c)
	if err != nil {
		t.Fatalf("GetIdentity() error = %v", err)
	}
	if identity.UserID != 1234 || identity.EmployeeNumber != 1234 {
		t.Fatalf("identity=%+v, want uid 1234", identity)
	}
	if identity.UserName != "codex" || identity.Email != "codex@boe.local" {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestGetIdentityRejectsMissingEmployeeNumber(t *testing.T) {
	oldParser := ssoParser
	t.Cleanup(func() { ssoParser = oldParser })
	ssoParser = fakeParser{payload: &ssojwt.Payload{UserID: "999999"}}

	var c app.RequestContext
	c.Request.Header.Set(bytedanceUserHeader, "jwt-token")

	if _, err := GetIdentity(&c); err == nil {
		t.Fatalf("GetIdentity() error = nil, want missing employee number error")
	}
}
