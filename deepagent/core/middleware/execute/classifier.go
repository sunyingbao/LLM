package execute

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
)

// CommandClassifier classifies shell scripts before execution.
//
// Implementations should treat CommandSpec.RawCommand as the source of truth.
// If RawCommand is empty, the command cannot be safely classified as a shell
// script and should be reported as unknown.
type CommandClassifier interface {
	Classify(ctx context.Context, spec CommandSpec) (CommandClassification, error)
}

type DefaultClassifier struct{}

func NewDefaultClassifier() *DefaultClassifier {
	return &DefaultClassifier{}
}

func (c *DefaultClassifier) Classify(ctx context.Context, spec CommandSpec) (CommandClassification, error) {
	script := strings.TrimSpace(spec.RawCommand)
	if script == "" {
		return CommandClassification{
			Class:  CommandUnknown,
			Reason: "raw shell command is required for classification",
		}, nil
	}
	return classifyShellScript(script), nil
}

func classifyShellScript(script string) CommandClassification {
	// This classifier is deliberately conservative. It is not a complete shell
	// parser; it only recognizes the simple command shapes that we are willing
	// to call safe. Anything structurally unclear becomes unknown/dangerous and
	// is denied by ReadOnlyPolicy.
	//
	// The flow is:
	//   1. Reject whole-script shell features that can hide writes or arbitrary
	//      execution before we look at individual commands.
	//   2. Split the script into simple commands on top-level |, &&, ||, and ;.
	//   3. Classify each simple command by argv and use the most severe result
	//      as the classification for the entire script.
	if containsShellSubstitution(script) {
		return CommandClassification{
			Class:  CommandDangerous,
			Reason: "shell substitution is not allowed in read-only command classification",
		}
	}
	if containsOutputRedirection(script) {
		return CommandClassification{
			Class:  CommandDangerous,
			Reason: "output redirection may write files",
		}
	}

	parts := splitShellCommands(script)
	if len(parts) == 0 {
		return CommandClassification{
			Class:  CommandUnknown,
			Reason: "empty command",
		}
	}

	overall := CommandSafe
	reason := "known safe command"
	commands := make([]SimpleCommand, 0, len(parts))
	for _, part := range parts {
		argv := tokenizeShellWords(part)
		if len(argv) == 0 {
			continue
		}
		commands = append(commands, SimpleCommand{Argv: argv})
		class, subReason := classifySimpleCommand(argv)
		if severity(class) > severity(overall) {
			overall = class
			reason = subReason
		}
	}
	if len(commands) == 0 {
		return CommandClassification{
			Class:  CommandUnknown,
			Reason: "empty command",
		}
	}

	primary := ""
	if len(commands[0].Argv) > 0 {
		primary = filepath.Base(commands[0].Argv[0])
	}
	return CommandClassification{
		Class:          overall,
		Reason:         reason,
		FirstProgram:   primary,
		SimpleCommands: commands,
	}
}

func classifySimpleCommand(argv []string) (CommandClass, string) {
	if len(argv) == 0 {
		return CommandUnknown, "empty command"
	}
	cmd := filepath.Base(argv[0])
	switch cmd {
	case "bash", "sh", "zsh":
		if script, ok := shellInlineScript(argv); ok {
			classification := classifyShellScript(script)
			return classification.Class, classification.Reason
		}
		return CommandUnknown, "shell invocation is not a known safe command"
	case "pwd", "ls", "cat", "head", "tail", "wc", "nl", "grep", "echo", "true", "false", "whoami", "uname":
		return CommandSafe, "known safe command"
	case "rg":
		return classifyRipgrep(argv)
	case "sed":
		return classifySed(argv)
	case "git":
		return classifyGit(argv)
	case "find":
		return classifyFind(argv)
	case "rm":
		if rmTargetsRoot(argv) {
			return CommandForbidden, "refusing destructive root removal"
		}
		return CommandDangerous, "rm may delete files"
	case "mv", "cp", "chmod", "chown", "mkdir", "touch", "ln", "tee", "xargs", "curl", "wget", "ssh", "scp":
		return CommandDangerous, cmd + " may mutate files, run arbitrary commands, or access external systems"
	case "python", "python3":
		if containsArg(argv[1:], "-c") {
			return CommandDangerous, "python -c executes arbitrary code"
		}
		return CommandUnknown, "python script execution is not classified as safe"
	case "node":
		if containsArg(argv[1:], "-e") {
			return CommandDangerous, "node -e executes arbitrary code"
		}
		return CommandUnknown, "node script execution is not classified as safe"
	default:
		return CommandUnknown, "unknown command"
	}
}

func classifyRipgrep(argv []string) (CommandClass, string) {
	for _, arg := range argv[1:] {
		switch {
		case arg == "--pre", strings.HasPrefix(arg, "--pre="):
			return CommandDangerous, "rg --pre executes an external command"
		case arg == "--hostname-bin", strings.HasPrefix(arg, "--hostname-bin="):
			return CommandDangerous, "rg --hostname-bin executes an external command"
		case arg == "--search-zip", arg == "-z":
			return CommandRisky, "rg zip search may call decompression tools"
		}
	}
	return CommandSafe, "rg without unsafe options is read-only"
}

var sedPrintRange = regexp.MustCompile(`^'?([0-9]+,)?[0-9]+p'?$`)

func classifySed(argv []string) (CommandClass, string) {
	if len(argv) <= 4 && len(argv) >= 3 && argv[1] == "-n" && sedPrintRange.MatchString(argv[2]) {
		return CommandSafe, "sed -n print range is read-only"
	}
	return CommandDangerous, "sed command is not a recognized read-only print"
}

func classifyFind(argv []string) (CommandClass, string) {
	unsafe := map[string]struct{}{
		"-exec": {}, "-execdir": {}, "-ok": {}, "-okdir": {},
		"-delete": {}, "-fls": {}, "-fprint": {}, "-fprint0": {}, "-fprintf": {},
	}
	for _, arg := range argv[1:] {
		if _, ok := unsafe[arg]; ok {
			return CommandDangerous, "find option may mutate files or execute commands"
		}
	}
	return CommandSafe, "find without mutating options is read-only"
}

func classifyGit(argv []string) (CommandClass, string) {
	if gitHasConfigOverride(argv) {
		return CommandDangerous, "git config override may execute external commands"
	}
	idx, sub := findGitSubcommand(argv)
	if idx < 0 {
		return CommandUnknown, "git subcommand is not classified as safe"
	}
	args := argv[idx+1:]
	switch sub {
	case "status", "log", "diff", "show":
		if gitArgsHaveUnsafeFlags(args) {
			return CommandDangerous, "git subcommand contains unsafe flags"
		}
		return CommandSafe, "read-only git subcommand"
	case "branch":
		if gitArgsHaveUnsafeFlags(args) || !gitBranchReadOnly(args) {
			return CommandDangerous, "git branch arguments may mutate branches"
		}
		return CommandSafe, "read-only git branch query"
	default:
		return CommandUnknown, "git subcommand is not classified as safe"
	}
}

func splitShellCommands(script string) []string {
	var parts []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(script); i++ {
		ch := script[i]
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			cur.WriteByte(ch)
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			cur.WriteByte(ch)
			continue
		}
		if !inSingle && !inDouble {
			if i+1 < len(script) {
				two := script[i : i+2]
				if two == "&&" || two == "||" {
					appendShellPart(&parts, cur.String())
					cur.Reset()
					i++
					continue
				}
			}
			if ch == ';' || ch == '|' {
				appendShellPart(&parts, cur.String())
				cur.Reset()
				continue
			}
		}
		cur.WriteByte(ch)
	}
	appendShellPart(&parts, cur.String())
	return parts
}

func appendShellPart(parts *[]string, part string) {
	if trimmed := strings.TrimSpace(part); trimmed != "" {
		*parts = append(*parts, trimmed)
	}
}

func tokenizeShellWords(s string) []string {
	var out []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if !inSingle && !inDouble && (ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r') {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func containsShellSubstitution(s string) bool {
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle {
			continue
		}
		if ch == '`' || (ch == '$' && i+1 < len(s) && s[i+1] == '(') {
			return true
		}
	}
	return false
}

func containsOutputRedirection(s string) bool {
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble {
			continue
		}
		if ch == '>' {
			return true
		}
	}
	return false
}

func shellInlineScript(argv []string) (string, bool) {
	for i := 1; i < len(argv)-1; i++ {
		if argv[i] == "-c" || argv[i] == "-lc" {
			return argv[i+1], true
		}
	}
	return "", false
}

func gitHasConfigOverride(argv []string) bool {
	for _, arg := range argv[1:] {
		if arg == "-c" || strings.HasPrefix(arg, "-c") && len(arg) > 2 || arg == "--config-env" || strings.HasPrefix(arg, "--config-env=") {
			return true
		}
	}
	return false
}

func findGitSubcommand(argv []string) (int, string) {
	safe := map[string]struct{}{"status": {}, "log": {}, "diff": {}, "show": {}, "branch": {}}
	skipNext := false
	for i, arg := range argv[1:] {
		idx := i + 1
		if skipNext {
			skipNext = false
			continue
		}
		if gitGlobalOptionTakesValue(arg) {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if _, ok := safe[arg]; ok {
			return idx, arg
		}
		return -1, ""
	}
	return -1, ""
}

func gitGlobalOptionTakesValue(arg string) bool {
	return arg == "-C" || arg == "--git-dir" || arg == "--work-tree" || arg == "--exec-path" || strings.HasPrefix(arg, "-C") && len(arg) > 2 || strings.HasPrefix(arg, "--git-dir=") || strings.HasPrefix(arg, "--work-tree=") || strings.HasPrefix(arg, "--exec-path=")
}

func gitArgsHaveUnsafeFlags(args []string) bool {
	for _, arg := range args {
		if arg == "--output" || strings.HasPrefix(arg, "--output=") || arg == "--ext-diff" || arg == "--textconv" || arg == "--exec" || strings.HasPrefix(arg, "--exec=") || arg == "--paginate" {
			return true
		}
	}
	return false
}

func gitBranchReadOnly(args []string) bool {
	if len(args) == 0 {
		return true
	}
	for _, arg := range args {
		switch {
		case arg == "--list", arg == "-l", arg == "--show-current", arg == "-a", arg == "--all", arg == "-r", arg == "--remotes", arg == "-v", arg == "-vv", arg == "--verbose":
		case strings.HasPrefix(arg, "--format="):
		default:
			return false
		}
	}
	return true
}

func rmTargetsRoot(argv []string) bool {
	for _, arg := range argv[1:] {
		if arg == "/" {
			return true
		}
	}
	return false
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func severity(class CommandClass) int {
	switch class {
	case CommandSafe:
		return 1
	case CommandRisky:
		return 2
	case CommandUnknown:
		return 3
	case CommandDangerous:
		return 4
	case CommandForbidden:
		return 5
	default:
		return 3
	}
}
