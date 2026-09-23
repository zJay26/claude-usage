# Claude Usage

[English](README.en.md) · [下载](https://github.com/zJay26/claude-usage/releases/latest) · [合成数据演示](https://zjay26.github.io/claude-usage/)

本地 Claude Token 仪表盘。一个程序，内嵌 Web 界面和 SQLite，无需 Node.js、账号登录或 API Key。支持 Windows、WSL、Linux 和 macOS。

![Claude Usage 概览，全部为合成数据](docs/images/dashboard.png)

## 使用

从 Releases 下载对应操作系统和架构的程序。Windows 首次运行可双击打开；终端运行以下命令可保持前台运行，不安装服务：

```powershell
.\claude-usage-windows-amd64.exe serve
```

浏览器访问 **http://127.0.0.1:43191**。Linux/macOS 下载后先 `chmod +x claude-usage-*`，再运行 `./claude-usage-linux-amd64 serve`（macOS 替换为 `darwin-arm64` 或 `darwin-amd64`）。下载文件的 SHA-256 可与发布页的 `SHA256SUMS` 核对。

需要开机自动运行时，主动执行 `claude-usage install`。Windows 使用当前用户启动项，Linux/WSL 使用 systemd 用户服务，macOS 使用 LaunchAgent。`uninstall` 停止并移除服务，保留统计；`uninstall --purge` 还会删除本工具的状态目录。

```text
claude-usage                     打开 Dashboard
claude-usage serve               前台运行
claude-usage scan --json         扫描并输出结果
claude-usage summary --json      查询统计
claude-usage doctor              诊断来源、数据库和本地服务
claude-usage update check        检查版本
claude-usage update install      用户主动下载安装更新
claude-usage version             显示版本
```

运行 `claude-usage help` 查看完整命令。Dashboard 更新面板也支持检查、下载目录设置、SHA-256 校验、备份及失败恢复；自动检查不会自动安装。

## 来源与计量

- 默认读取 `~/.claude/projects`；设置 `CLAUDE_CONFIG_DIR` 可指定替代目录，界面的“日志来源”可添加多个目录。
- Windows 默认自动发现用户 WSL 发行版，排除 Docker 内部发行版，允许启动未运行的发行版。可在“日志来源”关闭发现或启动。查询使用发行版默认用户和有超时的隐藏进程。
- 读取主会话、`subagents`、`.orphaned*.jsonl` 和 `.jsonl.superseded-*` 转录副本；只接受 Claude 模型及已识别的 Bedrock/Vertex 名称，排除第三方模型和 `<synthetic>`。
- 用请求与消息身份全局去重，后补身份可合并已有记录；一个请求可关联多个来源。组合筛选取并集，Windows/WSL 镜像只计一次。
- `input = 普通输入 + 缓存读取 + 缓存写入`；`total = input + output`。Thinking 是 Output 的子集。缓存写入保留 5 分钟、1 小时和期限未知的区别。
- 非空有效 `usage.iterations` 替代顶层用量，压缩和 Advisor 按各自模型入账，不重复叠加顶层数据。部分用量可由后续完整记录补齐；冲突会给出诊断并保留已确认数据。
- 30 秒检查变化，10 分钟兜底扫描；支持事务、断点续扫、大内容行、半行等待、重启和幂等扫描。删除源日志不会删除历史。截断或改写已读取内容会保留统计，并请求明确同意重建。
- 任务关系只采用明确的会话元数据与子代理目录，不把消息 `parentUuid` 当作父任务；找不到父任务的子任务独立展示。

Dashboard 提供概览、每日月历、小时下钻、分钟范围、模型/项目/任务统计、任务树、搜索、组合筛选、JSON/CSV 导出、中英双语、明暗主题、字体与密度设置及移动端布局。

## 费用含义

费用是**按当前公开 API 单价计算的 Token 等价估算**，不是订阅扣额或真实账单，也不追溯历史价格。内置官方价格核验日期为 **2026-09-23**，包含 Opus 5.5、Sonnet 5 等型号。

普通输入、缓存读取、两种缓存写入和输出分别计价，金额采用整数纳美元。明确的 Fast 标记使用适用的官方 Fast 规则；未知模型、未知缓存期限和不适用的旧长上下文价格保留 Token，并显示未定价原因及覆盖率。新版完整上下文按当前标准单价；Sonnet 4/4.5 超过 200K 的旧上下文档位不沿用历史价格。

可以为未知 Claude 名称填写本机单价或映射到内置型号。价格修改在查询时生效，不改写日志或重新入账。

依据：[官方定价](https://platform.claude.com/docs/en/about-claude/pricing)、[Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)、[迭代计量](https://platform.claude.com/docs/en/about-claude/models/optimizing-for-cost-and-intelligence)。

## 隔离和隐私

不修改 Claude 配置，不收集凭据，不保存对话正文、Thinking 文本或工具输出。解析时流式跳过这些内容；统计库仅保存用量与请求、模型、时间、项目路径、任务标题、关系和来源元数据。项目路径和标题仍可能敏感，分享导出前请检查。

服务仅监听本机回环地址，页面资源随程序内嵌。没有遥测或外部上报；仅检查/下载更新时访问 GitHub。所有来源采用数据库首次创建时保存的计量时区，避免 Windows/WSL 各自计算日期。

| 设置 | 用途 |
| --- | --- |
| `CLAUDE_USAGE_HOME` | 独立状态目录（配置、统计库、更新状态） |
| `CLAUDE_USAGE_TIMEZONE` | 新数据库计量时区，例如 `Asia/Shanghai` |
| `CLAUDE_USAGE_LANG` | 终端语言 `zh-CN` / `en` |
| `CLAUDE_CONFIG_DIR` | 本机 Claude 日志根目录 |

默认状态目录：Windows `%LOCALAPPDATA%\claude-usage`；Linux/WSL `$XDG_DATA_HOME/claude-usage` 或 `~/.local/share/claude-usage`；macOS `~/Library/Application Support/claude-usage`。与 codex-usage 的程序、端口、数据、服务和浏览器设置独立。

## 开发与接口

```sh
go test ./...
go vet ./...
go build -trimpath -o claude-usage ./cmd/claude-usage
npm ci
npx playwright install chromium
CLAUDE_USAGE_BIN=./claude-usage npm test
```

Windows 设置 `$env:CLAUDE_USAGE_BIN` 后运行 `npm test`。先构建程序，避免把首次编译耗时误判为浏览器启动失败。`npm run build:demo` 生成纯合成数据演示，`npm run capture:media` 生成脱敏截图。`scripts/build.sh` / `scripts/build.ps1` 构建六个平台资产；CI 运行 Linux race 检查及 macOS 原生服务生命周期验证。

保留 `/api/v1`：`status`、`summary`、`timeseries`、`breakdown`、`dimensions`、`sessions`、`session-tree`、`session-estimates`、`cost-estimate`、`export`、`pricing`、`pricing/overrides`、`rescan`、`updates`。新增 `GET/PUT /api/v1/sources`。筛选可重复传入 `home=...`，另支持 `since`、`until`、`date`、`model`、`project`、`source`、`agent_type`、`mode`、`session_id` 和 `q`。

Dashboard 费用使用 `cost_basis=claude_fast_weighted`；兼容的 Standard 对比口径为 `current_standard_api_text_token_prices`。JSON 导出包含缓存期限、迭代类别和来源列表；CSV 提供对应平面列，导出不写入估算金额。详见 [计量设计](docs/accounting.md) 和 [验证说明](docs/validation.md)。

## 来源与许可

独立项目，基于 [zJay26/codex-usage](https://github.com/zJay26/codex-usage) 的提交 `e0ca1f916097aff4a12f73e54152f9480655c9e6`。复用 Go/SQLite、内嵌 Dashboard、安装和更新框架；Claude 采集器、全局请求账本、来源管理与定价为本项目实现。保留原 MIT 声明，详见 [LICENSE](LICENSE) 与 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。非 Anthropic 官方产品。
