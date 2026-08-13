package constant

// ==================== 文件系统工具描述 ====================

// ToolLsDesc ls 工具描述

const ToolApplyPatchDesc = `## apply_patch
Use the apply_patch tool to edit files. Pass the complete patch in the patch argument.
The patch language is a stripped-down, file-oriented diff format designed to be easy to parse and safe to apply. Think of it as a high-level envelope:

*** Begin Patch
[ one or more file sections ]
*** End Patch

Within that envelope, you get a sequence of file operations.
You MUST include a header to specify the action you are taking.
Each operation starts with one of three headers:

*** Add File: <path> - create a new file. Every following line is a + line (the initial contents).
*** Delete File: <path> - remove an existing file. Nothing follows.
*** Update File: <path> - patch an existing file in place (optionally with a rename).

May be immediately followed by *** Move to: <new path> if you want to rename the file.
Then one or more hunks, each introduced by @@. Prefer bare @@.
If the location is ambiguous, add exact unchanged space-prefixed context lines inside the hunk.
Do not use line numbers or unified diff ranges after @@, such as @@ 78,16 or @@ -1,3 +1,4.

Within a hunk each line starts with exactly one of:

- ' ' (a single leading space): an unchanged context line
- '-': a line to delete
- '+': a line to add

Use bare @@ on its own line. The hunk body starts on the next line.
Every hunk body line has this shape: <operation><file line text>. The operation is exactly one extra first character and is never part of the file line text. Do not start a hunk line by copying the file line directly.
Before calling apply_patch, inspect every hunk body line character by character: character 1 must be the operation, and character 2 starts the exact file line text. There is no separator between them. Do not add a space after + or - unless that space is really the first character of the file line.
Choose the operation from your edit intent: space for copied context, - for an old line to delete, + for a new line to add. Then append the complete file line text unchanged after that operation. If the file line text begins with -, +, #, @, >, |, whitespace, or any other character, keep that character after the operation.
Common Markdown trap: a list bullet "-" is part of the file line text. To replace "- old" with "- new", write "-- old" then "+- new"; writing "- old" deletes " old" without the bullet.
For Markdown edits, prefer small separate @@ hunks around each exact replacement. Avoid adding context lines unless needed for ambiguity.
If you are changing an existing line, do not write the old line as copied context followed by +new. The old line must be a - line and the new line must be the following + line.
A single @@ hunk must describe one contiguous block from the original file. For edits in separate locations, use separate @@ hunks unless you include every unchanged line between them as space-prefixed context.
For copied context, the hunk line has one more leading space than the file line, because the operation itself is a space.
A top-level copied context line with no indentation still needs the space operation: write " topLevel:", not "topLevel:".
Examples of the shape:
- file line "plain" as context is " plain"; deleting it is "-plain"; adding it is "+plain".
- file line "- item" as context is " - item"; deleting it is "-- item"; adding it is "+- item".
- file line "+ value" as context is " + value"; deleting it is "-+ value"; adding it is "++ value".
- file line "    indented" as context is "     indented"; deleting it is "-    indented".
The full grammar definition is below:
Patch := Begin { FileOp } End
Begin := "*** Begin Patch" NEWLINE
End := "*** End Patch" NEWLINE
FileOp := AddFile | DeleteFile | UpdateFile
AddFile := "*** Add File: " path NEWLINE { "+" line NEWLINE }
DeleteFile := "*** Delete File: " path NEWLINE
UpdateFile := "*** Update File: " path NEWLINE [ MoveTo ] { Hunk }
MoveTo := "*** Move to: " newPath NEWLINE
Hunk := "@@" [ exactContextLine ] NEWLINE HunkLine { HunkLine } [ "*** End of File" NEWLINE ]
HunkLine := (" " | "-" | "+") text NEWLINE

A full patch can combine several operations:

*** Begin Patch
*** Add File: hello.txt
+Hello world
*** Update File: src/app.py
*** Move to: src/main.py
@@
 def greet():
-    print("Hi")
+    print("Hello, world!")
*** Delete File: obsolete.txt
*** End Patch

It is important to remember:

- You must include a header with your intended action (Add/Delete/Update)
- You must prefix new lines with + even when creating a new file
- There is no separator after + or -. For a file line "text", write "-text" or "+text", not "- text" or "+ text".
- Before calling apply_patch, check any edited file line that itself starts with "-" or "+": the hunk line needs the operation first, then that original symbol, for example "-- old" and "+- new" for Markdown list replacements.
- Separate edits that are not adjacent in the original file need separate @@ hunks, unless the hunk includes all unchanged lines between them as context.
- File references can only be relative, NEVER ABSOLUTE.
- The patch must end with *** End Patch. Do not omit it.
- Do not wrap the patch in markdown fences or shell commands.

## Examples

All paths in the patch must be relative to your current working directory.


### Add file example
*** Begin Patch
*** Add File: docs/hello.txt
+Hello, world!
*** End Patch

### Update Example
*** Begin Patch
*** Update File: a.txt
@@
-line2
+changed
*** End Patch

### Update with context lines
*** Begin Patch
*** Update File: src/app.py
@@
 class Greeter:
     def greet(self):
-        print("Hi")
+        print("Hello, world!")
*** End Patch

### Multiple operations example

*** Begin Patch
*** Add File: nested/new.txt
+created
*** Update File: notes.txt
@@
-old
+new
*** Delete File: delete_me.txt
*** End Patch

Call the tool like:
apply_patch({"patch":"*** Begin Patch\n*** Add File: hello.txt\n+Hello, world!\n*** End Patch\n"})
`

const ToolLsDesc = "列出指定目录的内容，返回文件和子目录列表"

// ToolReadFileDesc read_file 工具描述
const ToolReadFileDesc = "读取指定文件的内容，支持指定行范围"

// ToolWriteFileDesc write_file 工具描述
const ToolWriteFileDesc = "将内容写入指定文件，如果文件存在会被覆盖"

// ToolEditFileDesc edit_file 工具描述
const ToolEditFileDesc = "编辑文件中的指定内容，通过字符串替换实现"

// ToolGlobDesc glob 工具描述
const ToolGlobDesc = "使用 glob 模式匹配文件，返回匹配的文件路径列表"

// ToolGrepDesc grep 工具描述
const ToolGrepDesc = "在文件中搜索指定模式，返回匹配的行"

// ToolExecuteDesc execute 工具描述
const ToolExecuteDesc = "执行 shell 命令并返回输出结果"

// ToolUploadFilesDesc upload_files 工具描述
const ToolUploadFilesDesc = `上传文件到工作目录。
参数 files 是一个数组，每个元素包含：
- path: 目标路径
- content: 文件内容（base64 编码）`

// ToolDownloadFilesDesc download_files 工具描述
const ToolDownloadFilesDesc = `下载指定路径的文件。
参数 paths 是路径数组，返回每个文件的内容（base64 编码）`

// ==================== Planning 工具描述 ====================

// ToolUpdatePlanDesc update_plan 工具描述
const ToolUpdatePlanDesc = `Updates the task plan.
Provide an optional explanation and a list of plan items, each with a step and status.
At most one step can be in_progress at a time.`

// ToolWriteTodosDesc write_todos 工具描述
const ToolWriteTodosDesc = `创建或更新任务列表。这会替换现有的所有任务。

参数格式示例:
{"todos": [{"content": "第一个任务"}, {"content": "第二个任务", "priority": 1}]}

注意: todos 必须是对象数组，每个对象包含 content 字段`

// ToolReadTodosDesc read_todos 工具描述
const ToolReadTodosDesc = "读取当前任务列表和状态"

// ToolUpdateTodoDesc update_todo 工具描述
const ToolUpdateTodoDesc = `更新任务状态，支持单个或批量更新。同时只能有一个任务处于 in_progress 状态。

单个更新: {"todo_id": "abc12345", "status": "completed"}
批量更新: {"updates": [{"todo_id": "abc12345", "status": "completed"}, {"todo_id": "def67890", "status": "in_progress"}]}

批量更新按顺序处理，状态机规则同样适用。`

// ToolDispatchTasksDesc dispatch_tasks 工具描述
const ToolDispatchTasksDesc = `将当前 in_progress 任务拆分为多个独立子任务并行执行。
所有子任务完成后，该任务自动标记为 completed。

使用限制（必须全部满足）：
- 每个子任务必须是原子化的短小操作（预计 5 步以内可完成）
- 子任务之间完全独立，无共享状态
- 每个子任务有明确的输入和预期输出
- 禁止将"开发前端模块"等大粒度任务作为子任务

子任务描述要求：
- 必须具体、明确，包含所有必要的上下文信息
- 明确说明期望的输出格式和内容
- 子代理无法看到主对话历史，描述必须自包含

参数格式:
{"todo_id": "abc12345", "subtasks": [{"description": "具体任务描述"}, {"description": "另一个具体任务", "agent_type": "explorer"}]}

子代理类型：
- executor（默认）: 完整执行能力，可读写文件和执行命令
- explorer: 只读调研，仅搜索和阅读文件

注意：todo_id 必须是当前 in_progress 的任务`

// ==================== Skills 工具描述 ====================

// ToolListSkillsDesc list_skills 工具描述
const ToolListSkillsDesc = "列出所有可用的技能及其描述"

// ToolActivateSkillDesc activate_skill 工具描述
const ToolActivateSkillDesc = "激活技能，获取详细的技能指南。参数 name 为技能名称。"

// ToolDeactivateSkillDesc deactivate_skill 工具描述
const ToolDeactivateSkillDesc = "停用技能。参数 name 为技能名称。"

// ==================== SubAgent 工具描述 ====================

// ToolTaskDesc task 工具描述
const ToolTaskDesc = "启动子代理执行任务。子代理有独立的上下文，会返回执行结果"

// ToolListSubAgentsDesc list_subagents 工具描述
const ToolListSubAgentsDesc = "列出所有可用的子代理及其描述"

// ==================== Web 工具描述 ====================

// ToolFetchURLDesc fetch_url 工具描述
const ToolFetchURLDesc = `Fetch content from a URL and convert HTML to markdown format.

This tool fetches web page content and converts it to clean markdown text,
making it easy to read and process HTML content. After receiving the markdown,
you MUST synthesize the information into a natural, helpful response for the user.

Returns:
- success: Whether the request succeeded
- url: The final URL after redirects
- markdown_content: The page content converted to markdown
- status_code: HTTP status code
- content_length: Length of the markdown content in characters

IMPORTANT: After using this tool:
1. Read through the markdown content
2. Extract relevant information that answers the user's question
3. Synthesize this into a clear, natural language response
4. NEVER show the raw markdown to the user unless specifically requested`

// ToolWebSearchDesc web_search 工具描述 (Tavily)
const ToolWebSearchDesc = `Search the web using Tavily for current information and documentation.

This tool searches the web and returns relevant results. After receiving results,
you MUST synthesize the information into a natural, helpful response for the user.

IMPORTANT: After using this tool:
1. Read through the 'content' field of each result
2. Extract relevant information that answers the user's question
3. Synthesize this into a clear, natural language response
4. Cite sources by mentioning the page titles or URLs
5. NEVER show the raw JSON to the user - always provide a formatted response`

// ToolWebSearchDuckDuckGoDesc web_search 工具描述 (DuckDuckGo)
const ToolWebSearchDuckDuckGoDesc = `Search the web using DuckDuckGo for current information.

This tool searches the web using DuckDuckGo HTML search, which supports Chinese and other languages.
After receiving results, you MUST synthesize the information into a natural, helpful response for the user.

IMPORTANT: After using this tool:
1. Read through the 'content' field of each result
2. Extract relevant information that answers the user's question
3. Synthesize this into a clear, natural language response
4. Cite sources by mentioning the page titles or URLs
5. NEVER show the raw JSON to the user - always provide a formatted response`

// ToolHTTPRequestDesc http_request 工具描述
const ToolHTTPRequestDesc = `Make HTTP requests to APIs and web services.

This tool makes HTTP requests and returns the response. Supports GET, POST, PUT, DELETE and other HTTP methods.

Returns:
- success: Whether the request succeeded (status < 400)
- status_code: HTTP status code
- headers: Response headers
- content: Response body (JSON parsed if possible, otherwise text)
- url: Final URL after redirects`
