# Codex 对齐视觉验收（逐页）

这份清单用于对齐 `deep_agent_sdk/webui` 与 Codex Desktop 的视觉/交互规范。配合 `run-visual-audit.mjs` 可以自动拍照与断言关键视觉约束。

## 1. 运行方式

```bash
cd /Users/bytedance/go/src/content/LLM
node deepagent/cmd/cloud_agent/deep_agent_sdk/webui/visual_audit/run-visual-audit.mjs
```

- 默认使用内置协议夹具，不依赖数据库、Consul 或模型服务；11 个场景仍加载真实 HTML/CSS/ES Modules，并通过真实 DOM 操作完成验收。
- 设置 `VISUAL_AUDIT_MOCK=0` 时访问 `BASE_URL=http://127.0.0.1:8080`，执行一个真实后端工作区冒烟场景。该场景使用 `domcontentloaded`，不会被持续打开的 timeline SSE 阻塞。
- 若环境未安装 `playwright` 或 `playwright-core`，脚本会直接跳过并输出提示。
- 脚本会自动尝试以下路径解析：
  - 当前工作目录的 `playwright` / `playwright-core`
  - 进程 `node` 全局模块目录（如 `$(npm root -g)` 的父目录）
  - `NPM_CONFIG_PREFIX` / `npm_config_prefix` 指定目录下的 `lib/node_modules`
  - `NODE_PATH` 指定目录
- 可快速临时运行：
  - `npm i -D playwright`
  - 再次执行上面命令（首次会下载浏览器）
- 已有 Playwright 安装路径可直接指定（支持目录或入口文件）：
  - `PLAYWRIGHT_MODULE_PATH=/path/to/node_modules/playwright node ...`
  - 或 `PLAYWRIGHT_MODULE_PATH=/path/to/node_modules/playwright/index.js node ...`
- CI 或验收严格模式（缺失依赖时报错退出）：
  - `VISUAL_AUDIT_STRICT=1 node ...`

## 2. 十一个验收状态

1. `01-empty-task.png`
2. `02-running-tool.png`
3. `03-plan-waiting.png`
4. `04-approval-waiting.png`
5. `05-files-preview.png`
6. `06-terminal-output.png`
7. `07-changes-diff-comment.png`
8. `08-error-state.png`
9. `09-archived-task.png`
10. `10-inspector-collapsed-1179x900.png`
11. `11-drawers-899x900.png`

场景覆盖空态隐藏、工具展开、Plan 回答、Approval 提交、文本文件预览、Terminal 投影、Diff 行评论、错误态、Task 重命名和归档、Inspector 键盘切换以及窄屏双抽屉。

## 3. 字体/尺寸对齐（来自 tokens.css）

- `--font-body: 13px`
- `--font-input: 14px`
- `--font-meta: 12px`
- `--font-code: 12px`
- `--line-body: 20px`
- `--line-input: 21px`
- `--line-meta: 16px`
- `--line-code: 18px`

## 4. 交互约束

- `inspector` 三栏结构必须为 `changes / files / terminal`。
- Sidebar Task 的 `aria-label`/按钮与操作按钮有可访问文本。
- 关键操作按钮具备 `aria-label`。

## 5. 产物

脚本会在 `webui/visual_audit/.reports/<time>/` 输出上述 11 张截图和 `visual-summary.json`。

### 运行前端验收快照（可选）

```bash
bash deepagent/cmd/cloud_agent/deep_agent_sdk/webui/visual_audit/smoke.sh
```

会执行：
1. WebUI 所有静态单元测试
2. webui Go package 测试
3. 关键 service 包测试（当前已知与环境 socket 权限相关测试除外）
4. 视觉验收脚本（默认使用内置协议夹具；playwright 不存在时跳过）

## 6. 本地联调命令（示例）

启动服务示例：

```bash
cd /Users/bytedance/go/src/content/LLM
DEEP_AGENT_SDK_API_ADDRESS=:8080 GOCACHE=/tmp/gocache_llm_webui \
  go run ./deepagent/cmd/cloud_agent/deep_agent_sdk
```

本地环境若无法监听 0.0.0.0，可先保留已有服务并设置：

```bash
VISUAL_AUDIT_MOCK=0 AUTO_START_SERVER=0 BASE_URL=http://127.0.0.1:8080 \
  bash deepagent/cmd/cloud_agent/deep_agent_sdk/webui/visual_audit/smoke.sh
```

如果未设置 `PORT`，`smoke.sh` 会自动从 `BASE_URL` 推断端口（如 `...:8080`）。
`AUTO_START_SERVER=1` 会在命令内生成临时 hertz 配置，默认将 `DebugPort=0`、`EnablePprof=false`，避免本机缺少 pprof 监听端口时失败。

如果你只想验证前端静态验收，可直接配置：

```bash
BASE_URL=http://127.0.0.1:8080 \
VISUAL_AUDIT_MOCK=0 \
VISUAL_AUDIT_STRICT=0 \
node deepagent/cmd/cloud_agent/deep_agent_sdk/webui/visual_audit/run-visual-audit.mjs
```
