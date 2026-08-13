package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type approvalRequest struct {
	toolName string
	args     string
	reply    chan bool
}

const approvalPromptHeight = 3

func renderApprovalPrompt(req approvalRequest, width int) string {
	const argLimit = 200
	args := req.args
	if width > 0 && len(args) > width {
		args = args[:max(0, width-12)] + " …"
	} else if len(args) > argLimit {
		args = args[:argLimit] + fmt.Sprintf(" …(%d more bytes)", len(req.args)-argLimit)
	}
	header := approvalHeaderStyle.Render(fmt.Sprintf("● Approve tool %q?", req.toolName))
	argsLine := approvalArgsStyle.Render(args)
	hint := approvalHintStyle.Render("y = allow · n / Enter / Esc = deny · Ctrl-C = deny + abort")
	return strings.Join([]string{header, argsLine, hint}, "\n")
}

var (
	approvalHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	approvalArgsStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	approvalHintStyle   = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("241"))
)
