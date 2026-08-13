package filesystem

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"
	jsonschema "github.com/eino-contrib/jsonschema"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
)

const filesystemSystemPromptBaseEN = `## Filesystem Access

You can access the filesystem.

Available tools:
- ls: List directory contents
- read_file: Read file contents with optional pagination via offset and limit
- write_file: Create a new file or overwrite an existing file
- glob: Find files using glob patterns
- grep: Search text in files with regular expressions
- upload_files: Upload multiple files to the workspace`

const filesystemExecutePromptLineEN = `
- execute: Run shell commands. For commands that may run for a long time or are not expected to exit, wrap them with timeout and use a 60 second timeout.`

const filesystemSystemPromptSuffixEN = `

Guidelines:
1. When editing files, use exact matches and make sure old_string is unique
2. Use pagination for large files with offset and limit
3. Use glob to find files and grep to search file contents
4. Run commands carefully and avoid destructive operations
5. Your workspace path is %s. Treat this directory as the current path
`

type filesystemPromptCatalog struct {
	base         string
	executeLine  string
	suffixFormat string

	title           string
	intro           string
	toolsTitle      string
	guidelinesTitle string
	workspaceGuide  string

	toolPromptLines   map[string]string
	toolDescriptions  map[string]string
	paramDescriptions map[string]map[string]string

	editGuide    string
	readGuide    string
	searchGuide  string
	executeGuide string
}

var filesystemPromptCatalogZH = filesystemPromptCatalog{
	base:         constant.FilesystemSystemPromptBase,
	executeLine:  constant.FilesystemExecutePromptLine,
	suffixFormat: constant.FilesystemSystemPromptSuffix,

	title:           "## 文件系统访问",
	intro:           "你有访问文件系统的能力。",
	toolsTitle:      "可用工具：",
	guidelinesTitle: "使用指南：",
	workspaceGuide:  "你的工作区路径是 %s , 你可以理解为当前路径即是此目录",

	toolPromptLines: map[string]string{
		constant.ToolLs:            "- ls: 列出目录内容\n",
		constant.ToolReadFile:      "- read_file: 读取文件内容（支持分页：offset, limit）\n",
		constant.ToolWriteFile:     "- write_file: 创建新文件或覆盖现有文件\n",
		constant.ToolGlob:          "- glob: 使用模式匹配查找文件\n",
		constant.ToolGrep:          "- grep: 在文件中搜索文本（支持正则表达式）\n",
		constant.ToolUploadFiles:   "- upload_files: 批量上传文件到工作区\n",
		constant.ToolDownloadFiles: "- download_files: 批量下载工作区文件\n",
		constant.ToolExecute:       constant.FilesystemExecutePromptLine,
		constant.ToolApplyPatch:    "- apply_patch: 编辑文件（精确的多文件 patch 工具）\n",
		constant.ToolEditFile:      "- edit_file: 编辑文件（精确字符串替换）\n",
	},
	toolDescriptions: map[string]string{
		constant.ToolLs:            constant.ToolLsDesc,
		constant.ToolReadFile:      constant.ToolReadFileDesc,
		constant.ToolWriteFile:     constant.ToolWriteFileDesc,
		constant.ToolEditFile:      constant.ToolEditFileDesc,
		constant.ToolApplyPatch:    constant.ToolApplyPatchDesc,
		constant.ToolGlob:          constant.ToolGlobDesc,
		constant.ToolGrep:          constant.ToolGrepDesc,
		constant.ToolExecute:       constant.ToolExecuteDesc,
		constant.ToolUploadFiles:   constant.ToolUploadFilesDesc,
		constant.ToolDownloadFiles: constant.ToolDownloadFilesDesc,
	},

	editGuide:    "编辑文件时使用精确匹配，确保 old_string 唯一",
	readGuide:    "大文件使用分页读取（offset/limit 参数）",
	searchGuide:  "使用 glob 查找文件，使用 grep 搜索内容",
	executeGuide: "谨慎执行命令，避免破坏性操作",
}

var filesystemPromptCatalogEN = filesystemPromptCatalog{
	base:         filesystemSystemPromptBaseEN,
	executeLine:  filesystemExecutePromptLineEN,
	suffixFormat: filesystemSystemPromptSuffixEN,

	title:           "## Filesystem Access",
	intro:           "You can access the filesystem.",
	toolsTitle:      "Available tools:",
	guidelinesTitle: "Guidelines:",
	workspaceGuide:  "Your workspace path is %s. Treat this directory as the current path",

	toolPromptLines: map[string]string{
		constant.ToolLs:            "- ls: List directory contents\n",
		constant.ToolReadFile:      "- read_file: Read file contents with optional pagination via offset and limit\n",
		constant.ToolWriteFile:     "- write_file: Create a new file or overwrite an existing file\n",
		constant.ToolGlob:          "- glob: Find files using glob patterns\n",
		constant.ToolGrep:          "- grep: Search text in files with regular expressions\n",
		constant.ToolUploadFiles:   "- upload_files: Upload multiple files to the workspace\n",
		constant.ToolDownloadFiles: "- download_files: Download multiple files from the workspace\n",
		constant.ToolExecute:       filesystemExecutePromptLineEN,
		constant.ToolApplyPatch:    "- apply_patch: Edit files with an exact multi-file patch tool\n",
		constant.ToolEditFile:      "- edit_file: Edit files by exact string replacement\n",
	},
	toolDescriptions: map[string]string{
		constant.ToolLs:        "List the contents of a directory, returning files and subdirectories.",
		constant.ToolReadFile:  "Read the contents of a file, optionally with a line range.",
		constant.ToolWriteFile: "Write content to a file, overwriting the file if it already exists.",
		constant.ToolEditFile:  "Edit specific content in a file using exact string replacement.",
		constant.ToolApplyPatch: `Use apply_patch to edit files.
Pass the complete patch in the patch argument. The patch format is a file-oriented diff envelope:

*** Begin Patch
[one or more file sections]
*** End Patch

Each file operation starts with one of:
- *** Add File: <path>
- *** Delete File: <path>
- *** Update File: <path>

Inside an Update File hunk, each line after @@ must start with exactly one of:
- space: keep an existing line unchanged
- -: delete an existing line
- +: add a new line

Prefer bare @@. If the location is ambiguous, add exact unchanged space-prefixed context lines inside the hunk. Do not use line numbers or unified diff ranges after @@, such as @@ 78,16 or @@ -1,3 +1,4.

Use bare @@ on its own line. The hunk body starts on the next line. Every hunk body line has this shape: <operation><file line text>. The operation is exactly one extra first character and is never part of the file line text. Do not start a hunk line by copying the file line directly. Before calling apply_patch, inspect every hunk body line character by character: character 1 must be the operation, and character 2 starts the exact file line text. There is no separator between them. Do not add a space after + or - unless that space is really the first character of the file line. Choose the operation from your edit intent: space for copied context, - for an old line to delete, + for a new line to add. Then append the complete file line text unchanged after that operation. If the file line text begins with -, +, #, @, >, |, whitespace, or any other character, keep that character after the operation. Before calling apply_patch, check any edited file line that itself starts with "-" or "+": the hunk line needs the operation first, then that original symbol. Common Markdown trap: a list bullet "-" is part of the file line text. To replace "- old" with "- new", write "-- old" then "+- new"; writing "- old" deletes " old" without the bullet. For Markdown edits, prefer small separate @@ hunks around each exact replacement. Avoid adding context lines unless needed for ambiguity. If you are changing an existing line, do not write the old line as copied context followed by +new. The old line must be a - line and the new line must be the following + line. A single @@ hunk must describe one contiguous block from the original file. For edits in separate locations, use separate @@ hunks unless you include every unchanged line between them as space-prefixed context. For copied context, the hunk line has one more leading space than the file line, because the operation itself is a space. A top-level copied context line with no indentation still needs the space operation: write " topLevel:", not "topLevel:". Examples of the shape: file line "plain" as context is " plain", deleting it is "-plain", adding it is "+plain"; file line "- item" as context is " - item", deleting it is "-- item", adding it is "+- item"; file line "+ value" as context is " + value", deleting it is "-+ value", adding it is "++ value"; file line "    indented" as context is "     indented", deleting it is "-    indented". Use relative paths only. The patch must end with *** End Patch.`,
		constant.ToolGlob:    "Match files with a glob pattern and return matching file paths.",
		constant.ToolGrep:    "Search files for a pattern and return matching lines.",
		constant.ToolExecute: "Run a shell command and return its output.",
		constant.ToolUploadFiles: `Upload files to the workspace.
The files parameter is an array. Each item contains:
- path: target path
- content: file content as text or base64`,
		constant.ToolDownloadFiles: `Download files from the given paths.
The paths parameter is an array. The tool returns each file's content.`,
	},
	paramDescriptions: map[string]map[string]string{
		constant.ToolLs: {
			"path": "Directory path to list.",
		},
		constant.ToolReadFile: {
			"path":   "File path.",
			"offset": "Starting line number, zero-based. If omitted, reading starts from the beginning.",
			"limit":  "Number of lines to read. If omitted, read the full file.",
		},
		constant.ToolWriteFile: {
			"path":    "File path.",
			"content": "File content.",
		},
		constant.ToolEditFile: {
			"path":        "File path.",
			"old_string":  "Original string to replace. It must match exactly and be unique in the file.",
			"new_string":  "Replacement string.",
			"replace_all": "Whether to replace all matches. Defaults to replacing only the first match.",
		},
		constant.ToolApplyPatch: {
			"patch": "Complete patch content. It must start with *** Begin Patch and end with *** End Patch. Use bare @@ on its own line. The hunk body starts on the next line. Every hunk body line has shape <operation><file line text>. Character 1 is the operation; character 2 starts the exact file line text. There is no separator after + or -, so do not write + text or - text unless that space is actually part of the file line. The operation is one extra first character and is never part of the file line text. Choose the operation from edit intent, then append the exact file line unchanged; never let a file line's leading - or + serve as the operation. Changing an existing line requires -old followed by +new, not old as context followed by +new. A single @@ hunk must describe one contiguous block from the original file; edits in separate locations need separate @@ hunks unless all unchanged lines between them are included as context. A copied context line has one more leading space than the file line, even for top-level lines. For file line plain: context [space]plain, delete -plain, add +plain. For file line - item: context [space]- item, delete -- item, add +- item. For file line + value: context [space]+ value, delete -+ value, add ++ value. Do not use unified diff ranges like @@ 78,16 or @@ -1,3 +1,4.",
		},
		constant.ToolGlob: {
			"pattern": "Glob pattern, such as *.go or **/*.md.",
			"path":    "Starting path for the search. Defaults to the root path.",
		},
		constant.ToolGrep: {
			"pattern": "Search pattern as a regular expression.",
			"path":    "Search path, which can be a file or directory.",
			"glob":    "Optional filename filter pattern, such as *.go.",
		},
		constant.ToolExecute: {
			"command": "Shell command to run.",
		},
		constant.ToolUploadFiles: {
			"files":     "Files to upload.",
			"path":      "Target file path.",
			"content":   "File content as text or base64.",
			"is_base64": "Whether content is base64 encoded. Defaults to false.",
		},
		constant.ToolDownloadFiles: {
			"paths":     "File paths to download.",
			"as_base64": "Whether to return binary content as base64. Defaults to false.",
		},
	},

	editGuide:    "When editing files, use exact matches and make sure old_string is unique",
	readGuide:    "Use pagination for large files with offset and limit",
	searchGuide:  "Use glob to find files and grep to search file contents",
	executeGuide: "Run commands carefully and avoid destructive operations",
}

var filesystemPromptToolOrder = []string{
	constant.ToolLs,
	constant.ToolReadFile,
	constant.ToolWriteFile,
	constant.ToolGlob,
	constant.ToolGrep,
	constant.ToolUploadFiles,
	constant.ToolDownloadFiles,
	constant.ToolExecute,
	constant.ToolApplyPatch,
	constant.ToolEditFile,
}

func filesystemPromptText() filesystemPromptCatalog {
	if constant.IsEnglishPromptLang() {
		return filesystemPromptCatalogEN
	}
	return filesystemPromptCatalogZH
}

func (m *FilesystemMiddleware) buildDefaultPrompt() string {
	catalog := filesystemPromptText()
	var sb strings.Builder
	sb.WriteString(catalog.base)

	// 默认 prompt 不走 Tools(ctx)，避免为了构造提示词重复推导工具定义。
	if !m.readOnly && !m.disableExecute {
		if _, ok := m.backend.(backends.SandboxBackend); ok {
			sb.WriteString(catalog.executeLine)
		}
	}

	if m.applyPatchEnabled() {
		sb.WriteString(catalog.toolPromptLines[constant.ToolApplyPatch])
	} else {
		sb.WriteString(catalog.toolPromptLines[constant.ToolEditFile])
	}

	sb.WriteString(fmt.Sprintf(catalog.suffixFormat, m.workDir))
	return sb.String()
}

func (m *FilesystemMiddleware) buildMaskedPrompt(visible map[string]bool) string {
	catalog := filesystemPromptText()
	var sb strings.Builder
	sb.WriteString(catalog.title)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.intro)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.toolsTitle)
	sb.WriteString("\n")

	for _, toolName := range filesystemPromptToolOrder {
		if visible[toolName] {
			sb.WriteString(catalog.toolPromptLines[toolName])
		}
	}

	guides := filesystemPromptGuides(visible, catalog)
	sb.WriteString("\n")
	sb.WriteString(catalog.guidelinesTitle)
	sb.WriteString("\n")
	for i, guide := range guides {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, guide))
	}
	workspaceGuide := fmt.Sprintf(catalog.workspaceGuide, m.workDir)
	sb.WriteString(fmt.Sprintf("%d. %s\n", len(guides)+1, workspaceGuide))

	return sb.String()
}

func filesystemPromptGuides(visible map[string]bool, catalog filesystemPromptCatalog) []string {
	var guides []string
	if visible[constant.ToolEditFile] || visible[constant.ToolApplyPatch] || visible[constant.ToolWriteFile] {
		guides = append(guides, catalog.editGuide)
	}
	if visible[constant.ToolReadFile] {
		guides = append(guides, catalog.readGuide)
	}
	if visible[constant.ToolGlob] || visible[constant.ToolGrep] {
		guides = append(guides, catalog.searchGuide)
	}
	if visible[constant.ToolExecute] {
		guides = append(guides, catalog.executeGuide)
	}
	return guides
}

func filesystemToolDesc(toolName string) string {
	return filesystemPromptText().toolDescriptions[toolName]
}

func filesystemToolOptions(toolName string) []utils.Option {
	if !constant.IsEnglishPromptLang() {
		return nil
	}
	return []utils.Option{utils.WithSchemaModifier(filesystemEnglishSchemaModifier(toolName))}
}

func filesystemEnglishSchemaModifier(toolName string) utils.SchemaModifierFn {
	return func(jsonTagName string, _ reflect.Type, _ reflect.StructTag, schema *jsonschema.Schema) {
		if schema == nil {
			return
		}
		if desc := filesystemEnglishParamDesc(toolName, jsonTagName); desc != "" {
			schema.Description = desc
		}
	}
}

func filesystemEnglishParamDesc(toolName string, jsonTagName string) string {
	if fields := filesystemPromptCatalogEN.paramDescriptions[toolName]; fields != nil {
		return fields[jsonTagName]
	}
	return ""
}
