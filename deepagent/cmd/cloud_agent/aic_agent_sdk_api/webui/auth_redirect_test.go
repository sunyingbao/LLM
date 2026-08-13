package webui

import (
	"strings"
	"testing"
)

func TestAPIJSRedirectsUnauthenticatedToOIDCLogin(t *testing.T) {
	buf, err := Static.ReadFile("static/api.js")
	if err != nil {
		t.Fatalf("read static/api.js: %v", err)
	}
	source := string(buf)
	for _, want := range []string{
		`const LOGIN_PATH = "/oidc/login";`,
		"function redirectToLogin()",
		"response.status === 401",
		"next=${encodeURIComponent(next)}",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("static/api.js missing %q", want)
		}
	}
}

func TestAPIJSReadsSSOUserInfoDirectly(t *testing.T) {
	buf, err := Static.ReadFile("static/api.js")
	if err != nil {
		t.Fatalf("read static/api.js: %v", err)
	}
	source := string(buf)
	for _, want := range []string{
		`fetch("/userinfo"`,
		`credentials: "include"`,
		"function normalizeUserInfo(payload)",
		"function safeImageURL(value)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("static/api.js missing %q", want)
		}
	}
}

func TestAPIJSAddsBOETestAuthHeaderToAICAgentSDKRequests(t *testing.T) {
	buf, err := Static.ReadFile("static/api.js")
	if err != nil {
		t.Fatalf("read static/api.js: %v", err)
	}
	source := string(buf)
	for _, want := range []string{
		`const BOE_TEST_AUTH_HEADER = "X-AIC-Agent-SDK-Test-UID";`,
		`function aicAgentSDKHeaders(extra = {})`,
		`[BOE_TEST_AUTH_HEADER]: "1234"`,
		`headers: aicAgentSDKHeaders({ "Content-Type": "application/json" })`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("static/api.js missing %q", want)
		}
	}
}

func TestAppJSDropsStaleProjectAndSessionLoads(t *testing.T) {
	buf, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read static/app.js: %v", err)
	}
	source := string(buf)
	for _, want := range []string{
		"const projectName = state.selectedProjectName;",
		"if (projectName !== state.selectedProjectName) return;",
		"const selectedSessionID = normalizeID(sessionID);",
		"if (selectedSessionID !== normalizeID(state.selectedSessionID)) return;",
		"await selectProject(name);",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("static/app.js missing %q", want)
		}
	}
}

func TestWebUIHasUserInfoMountPointAndStyles(t *testing.T) {
	index, err := Static.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read static/index.html: %v", err)
	}
	styles, err := Static.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read static/styles.css: %v", err)
	}
	combined := string(index) + "\n" + string(styles)
	for _, want := range []string{
		`id="userInfo"`,
		".user-pill",
		".user-avatar",
		".user-email",
		"max-width: 30px;",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("webui static assets missing %q", want)
		}
	}
}

func TestWebUIHasFavicon(t *testing.T) {
	index, err := Static.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read static/index.html: %v", err)
	}
	icon, err := Static.ReadFile("static/favicon.svg")
	if err != nil {
		t.Fatalf("read static/favicon.svg: %v", err)
	}
	for _, want := range []string{
		`rel="icon"`,
		`href="./favicon.svg`,
		`<svg xmlns="http://www.w3.org/2000/svg"`,
	} {
		if !strings.Contains(string(index)+"\n"+string(icon), want) {
			t.Fatalf("webui favicon assets missing %q", want)
		}
	}
}
