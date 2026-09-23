package cliui

import (
	"fmt"
	"strings"
)

type Locale string

const (
	Chinese Locale = "zh-CN"
	English Locale = "en"
)

var messages = map[Locale]map[string]string{
	Chinese: {
		"error.prefix":         "错误:",
		"error.invalidLang":    "不支持语言 %q；可选值为 en 或 zh-CN",
		"error.langMissing":    "--lang 需要一个值；可选值为 en 或 zh-CN",
		"error.unknownCommand": "未知命令 %q",
		"usage": `Claude Usage — 当前电脑的 Claude Token 本地统计

用法:
  claude-usage [--lang en|zh-CN]       打开本机 Dashboard
  claude-usage open                    打开本机 Dashboard
  claude-usage install                 用户级安装、初始化 JSONL 扫描并启动
  claude-usage summary --since 7d      命令行摘要（支持 --json / --csv）
  claude-usage scan [--rebuild]        增量扫描本机 Claude session
  claude-usage doctor                  检查路径、JSONL 数据源与服务状态
  claude-usage config add-home PATH    添加一个额外 CLAUDE_CONFIG_DIR
  claude-usage uninstall [--purge]     卸载；默认保留统计库
  claude-usage serve                   前台运行本地服务
  claude-usage update check|install    检查或主动下载安装更新
  claude-usage version                 显示版本

语言:
  --lang 优先于 CLAUDE_USAGE_LANG，其次跟随系统语言；支持 en、zh-CN。

边界:
  “电脑”是运行 Claude 客户端和 claude-usage 的主机；不是 shell/tool 实际执行的远程环境。
  不读取 auth.json、prompt、回复、reasoning 或工具输出；不读取真实账单或账号配额，
  按当前公开 API 单价估算 Token 等价成本，不代表订阅扣额或真实账单。`,
		"serve.running":          "Claude Usage 正在 %s 运行；按 Ctrl+C 停止。\n",
		"open.start":             "启动本地服务: %w",
		"open.notReady":          "本地服务未在 12 秒内就绪；请运行 claude-usage doctor",
		"open.opened":            "已打开",
		"open.noGUI":             "无图形环境时，在你的电脑运行：ssh -N -L %d:127.0.0.1:%d %s@%s\n",
		"flag.rebuild":           "清空派生统计后从全部 JSONL 重建",
		"flag.json":              "输出 JSON",
		"flag.csv":               "输出 CSV",
		"flag.since":             "范围：7d、30d、today、all 或 RFC3339",
		"flag.skipScan":          "跳过首次历史扫描",
		"flag.purge":             "同时删除统计库和工具配置",
		"summary.conflict":       "--json 与 --csv 不能同时使用",
		"summary.title":          "Claude Usage · 当前电脑 · %s\n",
		"summary.cached":         "  Cached Input    %s  (Input 的子集)\n",
		"summary.reasoning":      "  Reasoning       %s  (Output 的子集)\n",
		"summary.unattributed":   "历史未归属        %s  (只属于累计，不属于所选日期)\n",
		"scan.complete":          "扫描完成：%d 个 Home，%d 个文件，新增 %d 个事件，忽略 %d 个重复，%d 条提示，耗时 %.2fs\n",
		"scan.unattributed":      "有 %d 个 session 的差额仅计入“历史未归属”，未伪造每日分布。\n",
		"install.stopCurrent":    "停止现有后台服务: %w",
		"install.permissions":    "警告：无法进一步收紧状态目录权限:",
		"install.installed":      "已安装:",
		"install.scanning":       "正在执行首次本地历史扫描（只解析 元数据与 Claude usage）…",
		"install.scanWarning":    "警告：首次扫描未完成:",
		"install.scanDone":       "首次扫描：%d 文件，新增 %d 事件，%d 条提示。\n",
		"install.health":         "后台服务未在 30 秒内通过本机 health check",
		"install.service":        "后台服务:",
		"install.warning":        "警告:",
		"install.done":           "安装完成。后台服务会定期增量扫描本机 JSONL；无需重启 Claude。",
		"doctor.loopbackOK":      "%s:%d；不会监听公网",
		"doctor.loopbackError":   "配置了非 loopback 地址",
		"doctor.permissions":     "状态目录已限制为当前用户访问",
		"doctor.machineHost":     "数据库最初属于主机 %s，当前主机为 %s；可能复制/同步了 CLAUDE_USAGE_HOME，逐电脑边界不再可靠",
		"doctor.machinePlatform": "数据库记录平台为 %s/%s，当前为 %s/%s；请勿跨电脑同步 CLAUDE_USAGE_HOME",
		"doctor.database":        "%s；events=%d sessions=%d",
		"doctor.jsonlOnly":       "JSONL 是唯一 Token 计数来源；状态库仅用于发现文件与补充 metadata",
		"doctor.coverage":        "%d 条异常/覆盖提示，请在 Dashboard 查看",
		"doctor.homeUnreadable":  "%s 不存在或不可读",
		"doctor.serviceOK":       "本地 Dashboard 可访问",
		"doctor.serviceDown":     "本地服务未运行",
		"doctor.privacy":         "数据库 schema 仅含计数、时间、模型、来源、路径和标题；无 prompt/reply/reasoning/auth 字段",
		"doctor.network":         "运行时无外部上报客户端；Dashboard 仅监听 loopback",
	},
	English: {
		"error.prefix":         "Error:",
		"error.invalidLang":    "unsupported language %q; use en or zh-CN",
		"error.langMissing":    "--lang requires a value; use en or zh-CN",
		"error.unknownCommand": "unknown command %q",
		"usage": `Claude Usage — local Claude token accounting for this machine

Usage:
  claude-usage [--lang en|zh-CN]       Open the local Dashboard
  claude-usage open                    Open the local Dashboard
  claude-usage install                 Install for this user, initialize JSONL scanning, and start
  claude-usage summary --since 7d      Print a CLI summary (supports --json / --csv)
  claude-usage scan [--rebuild]        Incrementally scan local Claude sessions
  claude-usage doctor                  Check paths, JSONL sources, and service state
  claude-usage config add-home PATH    Add another CLAUDE_CONFIG_DIR
  claude-usage uninstall [--purge]     Uninstall; keep the usage database by default
  claude-usage serve                   Run the local service in the foreground
  claude-usage version                 Print the version

Language:
  --lang overrides CLAUDE_USAGE_LANG, then the system locale; supports en and zh-CN.

Boundary:
  “Machine” means the host running the Claude client and claude-usage, not a remote shell/tool target.
  Claude Usage never reads auth.json, prompts, replies, reasoning, or tool output. It does not read
  bills or account quotas; cost is only a Standard API-equivalent estimate of local token usage.`,
		"serve.running":          "Claude Usage is running at %s; press Ctrl+C to stop.\n",
		"open.start":             "start local service: %w",
		"open.notReady":          "the local service was not ready within 12 seconds; run claude-usage doctor",
		"open.opened":            "Opened",
		"open.noGUI":             "With no graphical session, run this on your computer: ssh -N -L %d:127.0.0.1:%d %s@%s\n",
		"flag.rebuild":           "Clear derived accounting and rebuild it from all JSONL files",
		"flag.json":              "Output JSON",
		"flag.csv":               "Output CSV",
		"flag.since":             "Range: 7d, 30d, today, all, or RFC3339",
		"flag.skipScan":          "Skip the first historical scan",
		"flag.purge":             "Also delete the usage database and tool configuration",
		"summary.conflict":       "--json and --csv cannot be used together",
		"summary.title":          "Claude Usage · this machine · %s\n",
		"summary.cached":         "  Cached Input    %s  (subset of Input)\n",
		"summary.reasoning":      "  Reasoning       %s  (subset of Output)\n",
		"summary.unattributed":   "Historical only   %s  (part of all-time totals, not the selected dates)\n",
		"scan.complete":          "Scan complete: %d Homes, %d files, %d events added, %d duplicates ignored, %d notices, %.2fs\n",
		"scan.unattributed":      "%d sessions have deltas counted only as historical unattributed usage; no daily distribution was invented.\n",
		"install.stopCurrent":    "stop the current background service: %w",
		"install.permissions":    "Warning: could not further restrict the state directory:",
		"install.installed":      "Installed:",
		"install.scanning":       "Running the first local history scan (metadata and Claude usage only)…",
		"install.scanWarning":    "Warning: first scan did not complete:",
		"install.scanDone":       "First scan: %d files, %d events added, %d notices.\n",
		"install.health":         "the background service failed its local health check within 30 seconds",
		"install.service":        "Background service:",
		"install.warning":        "Warning:",
		"install.done":           "Installation complete. The background service incrementally scans local JSONL files; Claude does not need to restart.",
		"doctor.loopbackOK":      "%s:%d; not exposed publicly",
		"doctor.loopbackError":   "a non-loopback address is configured",
		"doctor.permissions":     "state directory access is restricted to the current user",
		"doctor.machineHost":     "database originally belonged to host %s; current host is %s. CLAUDE_USAGE_HOME may have been copied or synced, so the per-machine boundary is unreliable",
		"doctor.machinePlatform": "database platform is %s/%s; current platform is %s/%s. Do not sync CLAUDE_USAGE_HOME across machines",
		"doctor.database":        "%s; events=%d sessions=%d",
		"doctor.jsonlOnly":       "JSONL is the only token-accounting source; the state database is used only for discovery and metadata",
		"doctor.coverage":        "%d anomaly/coverage notices; review them in the Dashboard",
		"doctor.homeUnreadable":  "%s does not exist or is unreadable",
		"doctor.serviceOK":       "local Dashboard is reachable",
		"doctor.serviceDown":     "local service is not running",
		"doctor.privacy":         "database schema contains counts, time, model, source, path, and title only; no prompt/reply/reasoning/auth fields",
		"doctor.network":         "runtime has no external reporting client; the Dashboard listens on loopback only",
	},
}

func Normalize(value string) (Locale, bool) {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
	switch {
	case value == "en" || strings.HasPrefix(value, "en-"):
		return English, true
	case value == "zh" || strings.HasPrefix(value, "zh-"):
		return Chinese, true
	default:
		return "", false
	}
}

func Detect(explicit, environment, system string) (Locale, error) {
	if explicit != "" {
		locale, ok := Normalize(explicit)
		if !ok {
			return "", fmt.Errorf(messages[English]["error.invalidLang"], explicit)
		}
		return locale, nil
	}
	if locale, ok := Normalize(environment); ok {
		return locale, nil
	}
	if locale, ok := Normalize(system); ok {
		return locale, nil
	}
	return Chinese, nil
}

func (locale Locale) Text(key string, args ...any) string {
	if locale != English {
		locale = Chinese
	}
	format, ok := messages[locale][key]
	if !ok {
		format = messages[Chinese][key]
	}
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

func Catalogs() map[Locale]map[string]string { return messages }
