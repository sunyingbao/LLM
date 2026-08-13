package main

import (
	"context"
	"flag"

	"code.byted.org/gopkg/ctxvalues"
	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/kite/kitutil/logid"
	"code.byted.org/lang/gg/gptr"
	"code.byted.org/overpass/common/option/clientoption"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/capcut/business/common/sandbox_model"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/pippit/sandbox/gateway"
	"code.byted.org/overpass/pippit_sandbox_gateway/rpc/pippit_sandbox_gateway"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/backends/byted_sandbox"
)

var (
	psm     = flag.String("psm", "pippit.sandbox.gateway", "PSM for pippit sandbox gateway")
	bizType = flag.String("biz-type", "test", "BizType for sandbox session")
	bizID   = flag.String("biz-id", "first_test", "BizID for sandbox session (required)")
	uid     = flag.Int64("uid", 0, "UID for BizMeta (optional)")
	action  = flag.String("action", sandbox_model.BIZ_META_ACTION_SANDBOX_TOOL_CALL, "BizMeta Action (optional)")
)

// 用于收集不符合预期的测试用例
type UnexpectedCase struct {
	Description string
	Expected    string
	Actual      string
}

var unexpectedCases []UnexpectedCase

func main() {
	flag.Parse()

	ctx := context.Background()
	ctx = ctxvalues.SetLogID(ctx, logid.GetNginxID())

	if *bizID == "" {
		logs.Fatalf("-biz-id 不能为空")
		return
	}

	logs.Infof("=== AIInfraSandbox Backend 测试开始 ===")
	logs.Infof("PSM: %s", *psm)
	logs.Infof("BizType: %s", *bizType)
	logs.Infof("BizID: %s", *bizID)
	logs.Infof("UID: %d", *uid)
	logs.Infof("Action: %s", *action)

	// 初始化 overpass client 的默认 PSM（可选）
	if *psm != "" {
		pippit_sandbox_gateway.InitDefaultClientOptions(clientoption.WithPSM(*psm))
		logs.Infof("✓ pippit_sandbox_gateway client initialized")
	}

	meta := &sandbox_model.BizMeta{
		BizID:   *bizID,
		BizType: *bizType,
	}
	if *uid != 0 {
		meta.UID = uid
	}
	if *action != "" {
		meta.Action = action
	}

	sandboxBackend := byted_sandbox.NewAIInfraSandbox(meta, "/opt/tiger/workspace")
	if sandboxBackend == nil {
		logs.Fatal("Failed to create AIInfraSandbox backend - NewAIInfraSandbox returned nil")
		return
	}
	logs.Infof("✓ AIInfraSandbox backend created, id=%s", sandboxBackend.ID())

	// ==================== 测试用例 ====================
	// 1. 测试执行命令
	testExecuteCommand(ctx, sandboxBackend)
	// 2. 测试写入文件
	testWriteFile(ctx, sandboxBackend)
	// 3. 测试读取文件
	testReadFile(ctx, sandboxBackend)
	// 4. 测试编辑文件
	testEditFile(ctx, sandboxBackend)
	// 5. 测试 grep
	testGrep(ctx, sandboxBackend)
	// 6. 测试 GlobInfo
	testGlobInfo(ctx, sandboxBackend)
	// 7. 错误场景测试
	testErrorScenarios(ctx, sandboxBackend)

	queryRsp, _ := pippit_sandbox_gateway.RawCall.QueryBizSessionStatus(ctx, &gateway.QueryBizSessionStatusRequest{
		BizMeta: meta,
	})
	if queryRsp != nil {
		logs.Infof("session_id --> %s", queryRsp.SessionID)
	}

	logs.Infof("=== 所有测试完成 ===")

	// 输出不符合预期的测试用例
	if len(unexpectedCases) > 0 {
		logs.Infof("\n=== 不符合预期的测试用例 ===")
		for i, uc := range unexpectedCases {
			logs.Infof("%d. %s", i+1, uc.Description)
			logs.Infof("   预期: %s", uc.Expected)
			logs.Infof("   实际: %s", uc.Actual)
		}
	} else {
		logs.Infof("\n=== 所有测试用例都符合预期 ===")
	}
}

// 测试执行命令
func testExecuteCommand(ctx context.Context, backend backends.SandboxBackend) {
	logs.Infof("\n--- 测试执行命令 ---")

	// 测试简单命令
	result, err := backend.Execute(ctx, "echo 'Hello, World!'")
	if err != nil {
		logs.Errorf("✗ Execute command failed: %v", err)
	} else {
		logs.Infof("✓ Execute result: %s, ExitCode: %d", result.Output, result.ExitCode)
	}

	// 测试 ls 命令
	result, err = backend.Execute(ctx, "ls -la /")
	if err != nil {
		logs.Errorf("✗ Execute ls command failed: %v", err)
	} else {
		logs.Infof("✓ ls / result: %s, ExitCode: %d", result.Output, result.ExitCode)
	}

	// 测试 pwd 命令
	result, err = backend.Execute(ctx, "pwd")
	if err != nil {
		logs.Errorf("✗ Execute pwd command failed: %v", err)
	} else {
		logs.Infof("✓ pwd result: %s, ExitCode: %d", result.Output, result.ExitCode)
	}
}

// 测试写入文件
func testWriteFile(ctx context.Context, backend backends.SandboxBackend) {
	logs.Infof("\n--- 测试写入文件 ---")

	content := `第一行内容
第二行内容
第三行包含测试字符串
第四行内容
第五行内容`

	result, err := backend.Write(ctx, "/tmp/test.txt", content)
	if err != nil {
		logs.Errorf("✗ Write file failed: %v", err)
	} else if result.Error != "" {
		logs.Errorf("✗ Write file returned error: %v", result.Error)
	} else {
		logs.Infof("✓ Write file succeeded: Path=%s", result.Path)
	}

	// 写入另一个文件用于测试
	content2 := `这是一个测试文件
包含多个关键词：hello, world, test
另一个 hello 出现在这里`
	result, err = backend.Write(ctx, "/tmp/test2.txt", content2)
	if err != nil {
		logs.Errorf("✗ Write file2 failed: %v", err)
	} else if result.Error != "" {
		logs.Errorf("✗ Write file2 returned error: %v", result.Error)
	} else {
		logs.Infof("✓ Write file2 succeeded: Path=%s", result.Path)
	}
}

// 测试读取文件
func testReadFile(ctx context.Context, backend backends.SandboxBackend) {
	logs.Infof("\n--- 测试读取文件 ---")

	// 读取整个文件
	content, err := backend.Read(ctx, "/tmp/test.txt", gptr.Of(0), gptr.Of(100))
	if err != nil {
		logs.Errorf("✗ Read file failed: %v", err)
	} else {
		logs.Infof("✓ Read file succeeded:\n%s", content)
	}

	// 读取前 2 行
	content, err = backend.Read(ctx, "/tmp/test.txt", gptr.Of(0), gptr.Of(2))
	if err != nil {
		logs.Errorf("✗ Read file (2 lines) failed: %v", err)
	} else {
		logs.Infof("✓ Read file (2 lines) succeeded:\n%s", content)
	}

	// 从第 2 行开始读取 2 行
	content, err = backend.Read(ctx, "/tmp/test.txt", gptr.Of(1), gptr.Of(2))
	if err != nil {
		logs.Errorf("✗ Read file (offset 1, 2 lines) failed: %v", err)
	} else {
		logs.Infof("✓ Read file (offset 1, 2 lines) succeeded:\n%s", content)
	}
}

// 测试编辑文件
func testEditFile(ctx context.Context, backend backends.SandboxBackend) {
	logs.Infof("\n--- 测试编辑文件 ---")

	// 替换单个匹配（部分实现可能会替换所有；这里只做最小验证）
	result, err := backend.Edit(ctx, "/tmp/test.txt", "测试", "TEST", false)
	if err != nil {
		logs.Errorf("✗ Edit file (single) failed: %v", err)
	} else if result.Error != "" {
		logs.Errorf("✗ Edit file (single) returned error: %v", result.Error)
	} else {
		logs.Infof("✓ Edit file (single) succeeded: Path=%s, Occurrences=%d", result.Path, result.Occurrences)
	}

	// 读取文件查看结果
	content, _ := backend.Read(ctx, "/tmp/test.txt", gptr.Of(0), gptr.Of(10))
	logs.Infof("File content after single edit:\n%s", content)

	// 替换所有匹配
	result, err = backend.Edit(ctx, "/tmp/test2.txt", "hello", "HELLO", true)
	if err != nil {
		logs.Errorf("✗ Edit file (replace all) failed: %v", err)
	} else if result.Error != "" {
		logs.Errorf("✗ Edit file (replace all) returned error: %v", result.Error)
	} else {
		logs.Infof("✓ Edit file (replace all) succeeded: Path=%s, Occurrences=%d", result.Path, result.Occurrences)
	}

	// 读取文件查看结果
	content, _ = backend.Read(ctx, "/tmp/test2.txt", gptr.Of(0), gptr.Of(10))
	logs.Infof("File content after replace all:\n%s", content)
}

// 测试 grep
func testGrep(ctx context.Context, backend backends.SandboxBackend) {
	logs.Infof("\n--- 测试 Grep ---")

	// 在文件中搜索单个关键词
	matches, err := backend.GrepRaw(ctx, "TEST", "/tmp/test.txt", "")
	if err != nil {
		logs.Errorf("✗ Grep file failed: %v", err)
	} else {
		logs.Infof("✓ Grep file succeeded, found %d matches:", len(matches))
		for _, match := range matches {
			logs.Infof("  - Path: %s, Line: %d, Text: %s", match.Path, match.Line, match.Text)
		}
	}

	// 在目录中搜索
	matches, err = backend.GrepRaw(ctx, "HELLO", "/tmp", "*.txt")
	if err != nil {
		logs.Errorf("✗ Grep directory failed: %v", err)
	} else {
		logs.Infof("✓ Grep directory succeeded, found %d matches:", len(matches))
		for _, match := range matches {
			logs.Infof("  - Path: %s, Line: %d, Text: %s", match.Path, match.Line, match.Text)
		}
	}
}

// 测试 GlobInfo
func testGlobInfo(ctx context.Context, backend backends.SandboxBackend) {
	logs.Infof("\n--- 测试 GlobInfo ---")

	// 1. 在 /tmp 目录查找 *.txt 文件
	logs.Infof("1. 在 /tmp 目录查找 *.txt 文件")
	files, err := backend.GlobInfo(ctx, "*.txt", "/tmp")
	if err != nil {
		logs.Errorf("✗ GlobInfo failed: %v", err)
	} else {
		logs.Infof("✓ GlobInfo succeeded, found %d files:", len(files))
		for _, file := range files {
			logs.Infof("  - Path: %s, IsDir: %v, Size: %d", file.Path, file.IsDir, file.Size)
		}
	}

	// 2. 精确匹配 test.txt
	logs.Infof("2. 精确匹配 test.txt")
	files, err = backend.GlobInfo(ctx, "test.txt", "/tmp")
	if err != nil {
		logs.Errorf("✗ GlobInfo failed: %v", err)
	} else {
		logs.Infof("✓ GlobInfo succeeded, found %d files:", len(files))
		for _, file := range files {
			logs.Infof("  - Path: %s, IsDir: %v, Size: %d", file.Path, file.IsDir, file.Size)
		}
	}

	// 3. 查找不存在的模式 *.log
	logs.Infof("3. 查找不存在的模式 *.log")
	files, err = backend.GlobInfo(ctx, "*.log", "/tmp")
	if err != nil {
		logs.Errorf("✗ GlobInfo failed: %v", err)
	} else {
		logs.Infof("✓ GlobInfo succeeded, found %d files (expected 0)", len(files))
		for _, file := range files {
			logs.Infof("  - Path: %s, IsDir: %v, Size: %d", file.Path, file.IsDir, file.Size)
		}
	}

	// 4. 查找不存在的路径
	logs.Infof("4. 查找不存在的路径 /nonexistent/*.txt")
	files, err = backend.GlobInfo(ctx, "*.txt", "/nonexistent")
	if err != nil {
		logs.Infof("  ✓ 预期错误: %v", err)
	} else {
		logs.Errorf("  ✗ 应该返回错误，但找到 %d 个文件", len(files))
		for _, file := range files {
			logs.Infof("    - Path: %s, IsDir: %v, Size: %d", file.Path, file.IsDir, file.Size)
		}
	}

	// 5. 查找目录
	logs.Infof("5. 查找 /tmp 下的所有内容")
	files, err = backend.GlobInfo(ctx, "*", "/tmp")
	if err != nil {
		logs.Errorf("✗ GlobInfo failed: %v", err)
	} else {
		logs.Infof("✓ GlobInfo succeeded, found %d items (files + dirs):", len(files))
		for _, file := range files {
			logs.Infof("  - Path: %s, IsDir: %v, Size: %d", file.Path, file.IsDir, file.Size)
		}
	}
}

// 错误场景测试
func testErrorScenarios(ctx context.Context, backend backends.SandboxBackend) {
	logs.Infof("\n--- 错误场景测试 ---")

	// 1. 读取不存在的文件
	logs.Infof("1. 读取不存在的文件 /tmp/nonexistent.txt")
	content, err := backend.Read(ctx, "/tmp/nonexistent.txt", gptr.Of(0), gptr.Of(10))
	if err != nil {
		logs.Infof("  ✓ 预期错误: %v", err)
	} else {
		logs.Errorf("  ✗ 应该返回错误，但得到内容: %s", content)
	}

	// 2. 写入不存在的目录
	logs.Infof("2. 写入不存在的目录 /nonexistent/dir/test.txt")
	result, err := backend.Write(ctx, "/nonexistent/dir/test.txt", "test content")
	if err != nil {
		logs.Infof("  ✓ 预期错误: %v", err)
	} else if result.Error != "" {
		logs.Infof("  ✓ 预期错误: %v", result.Error)
	} else {
		logs.Errorf("  ✗ 应该返回错误，但写入成功: %s", result.Path)
		unexpectedCases = append(unexpectedCases, UnexpectedCase{
			Description: "写入不存在的目录",
			Expected:    "应该返回错误",
			Actual:      "写入成功",
		})
	}

	// 3. 编辑不存在的文件
	logs.Infof("3. 编辑不存在的文件 /tmp/nonexistent.txt")
	editResult, err := backend.Edit(ctx, "/tmp/nonexistent.txt", "old", "new", false)
	if err != nil {
		logs.Infof("  ✓ 预期错误: %v", err)
	} else if editResult.Error != "" {
		logs.Infof("  ✓ 预期错误: %v", editResult.Error)
	} else {
		logs.Errorf("  ✗ 应该返回错误，但编辑成功: Path=%s, Occurrences=%d", editResult.Path, editResult.Occurrences)
	}

	// 4. grep 不存在的文件
	logs.Infof("4. grep 不存在的文件 /tmp/nonexistent.txt")
	matches, err := backend.GrepRaw(ctx, "pattern", "/tmp/nonexistent.txt", "")
	if err != nil {
		logs.Infof("  ✓ 预期错误: %v", err)
	} else {
		logs.Infof("  结果: 找到 %d 个匹配 (可能是空数组)", len(matches))
		for _, match := range matches {
			logs.Infof("    - Path: %s, Line: %d, Text: %s", match.Path, match.Line, match.Text)
		}
	}

	// 5. 编辑不存在的字符串
	logs.Infof("5. 编辑文件中不存在的字符串")
	editResult, err = backend.Edit(ctx, "/tmp/test.txt", "不存在的字符串xyz", "new", false)
	if err != nil {
		logs.Infof("  错误: %v", err)
		if err.Error() == "exit status 126" {
			unexpectedCases = append(unexpectedCases, UnexpectedCase{
				Description: "编辑文件中不存在的字符串",
				Expected:    "返回空匹配或正常",
				Actual:      "exit status 126",
			})
		}
	} else if editResult.Error != "" {
		logs.Infof("  错误: %v", editResult.Error)
		if editResult.Error.Error() == "exit status 126" {
			unexpectedCases = append(unexpectedCases, UnexpectedCase{
				Description: "编辑文件中不存在的字符串",
				Expected:    "返回空匹配或正常",
				Actual:      "exit status 126",
			})
		}
	} else {
		logs.Infof("  结果: Occurrences=%d (预期为 0)", editResult.Occurrences)
	}

	// 6. 执行无效命令
	logs.Infof("6. 执行无效命令")
	cmdResult, err := backend.Execute(ctx, "invalid_command_that_does_not_exist")
	if err != nil {
		logs.Infof("  ✓ 预期错误: %v", err)
	} else {
		logs.Infof("  结果: Output=%s, ExitCode=%d", cmdResult.Output, cmdResult.ExitCode)
	}

	// 7. 读取目录（不是文件）
	logs.Infof("7. 读取目录 /tmp")
	content, err = backend.Read(ctx, "/tmp", gptr.Of(0), gptr.Of(10))
	if err != nil {
		logs.Infof("  ✓ 预期错误: %v", err)
	} else {
		logs.Infof("  结果: %s (应该返回错误或特殊处理)", content)
	}
}
