package sources

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

type Source struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Path     string `json:"path"`
	Platform string `json:"platform"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}

var cache struct {
	sync.Mutex
	key     string
	at      time.Time
	entries []Source
}

func Invalidate() { cache.Lock(); cache.at = time.Time{}; cache.Unlock() }
func Snapshot() []Source {
	cache.Lock()
	defer cache.Unlock()
	return append([]Source(nil), cache.entries...)
}

// A failed root is retried on the next scan without dropping its history.
func RecordResult(path string, err error, files ...int64) {
	cache.Lock()
	defer cache.Unlock()
	for i := range cache.entries {
		entry := &cache.entries[i]
		if entry.Path != path {
			continue
		}
		if err != nil {
			entry.State, entry.Error = "error", err.Error()
		} else {
			entry.State, entry.Error = "ready", ""
			if len(files) > 0 && files[0] == 0 {
				entry.State = "empty"
			}
		}
	}
}

// Discovery is cached independently of the 30-second transcript activity probe.
func Resolve(native []string, discover, start bool) []string {
	encoded, _ := json.Marshal([]any{native, discover, start})
	key := string(encoded)
	cache.Lock()
	defer cache.Unlock()
	if cache.key != key || time.Since(cache.at) >= 10*time.Minute {
		entries := make([]Source, 0, len(native))
		for _, p := range native {
			state := "ready"
			if _, e := os.Stat(filepath.Join(p, "projects")); e != nil {
				state = "empty"
			}
			entries = append(entries, Source{ID: p, Label: filepath.Base(p), Path: p, Platform: runtime.GOOS, State: state})
		}
		if runtime.GOOS == "windows" && discover {
			entries = append(entries, discoverWSL(start)...)
		}
		cache.entries, cache.key, cache.at = entries, key, time.Now()
	}
	seen := map[string]bool{}
	var paths []string
	for _, entry := range cache.entries {
		if entry.Path == "" || entry.State == "offline" {
			continue
		}
		key := entry.Path
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			paths = append(paths, entry.Path)
			seen[key] = true
		}
	}
	// Native homes are scanned first even when UNC paths sort before drive paths.
	sort.SliceStable(paths, func(i, j int) bool { return !IsWSLPath(paths[i]) && IsWSLPath(paths[j]) })
	return paths
}

func IsWSLPath(path string) bool {
	p := strings.ToLower(path)
	return strings.HasPrefix(p, `\\wsl.localhost\`) || strings.HasPrefix(p, `\\wsl$\`)
}
func command(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	HideCommand(cmd)
	cmd.WaitDelay = time.Second
	return cmd.Output()
}
func names(raw []byte) []string {
	text := string(raw)
	// wsl.exe writes UTF-16LE when redirected on Windows.
	if len(raw) > 1 && (strings.IndexByte(text, 0) >= 0 || (raw[0] == 0xff && raw[1] == 0xfe)) {
		r := make([]uint16, 0, len(raw)/2)
		for i := 0; i+1 < len(raw); i += 2 {
			v := uint16(raw[i]) | uint16(raw[i+1])<<8
			if v != 0xfeff {
				r = append(r, v)
			}
		}
		text = string(utf16.Decode(r))
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
func discoverWSL(start bool) []Source {
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		return nil
	}
	all, err := command(10*time.Second, "--list", "--quiet")
	if err != nil {
		return []Source{{ID: "wsl", Label: "WSL", Platform: "wsl", State: "error", Error: "WSL discovery unavailable"}}
	}
	runningRaw, _ := command(10*time.Second, "--list", "--running", "--quiet")
	running := map[string]bool{}
	for _, n := range names(runningRaw) {
		running[n] = true
	}
	var out []Source
	for _, name := range names(all) {
		if strings.HasPrefix(strings.ToLower(name), "docker-desktop") {
			continue
		}
		entry := Source{ID: "wsl:" + name, Label: name, Platform: "wsl", State: "ready"}
		if !start && !running[name] {
			entry.State = "offline"
			out = append(out, entry)
			continue
		}
		data, e := command(20*time.Second, "--distribution", name, "--exec", "/bin/sh", "-c", `printf '%s\n%s\n' "$HOME" "${CLAUDE_CONFIG_DIR:-}"`)
		if e != nil {
			entry.State = "error"
			entry.Error = "WSL start or home lookup failed / WSL 启动或目录查询失败"
		} else {
			parts := strings.Split(strings.TrimSpace(string(data)), "\n")
			home := strings.TrimSpace(parts[0])
			root := home + "/.claude"
			if len(parts) > 1 && strings.HasPrefix(strings.TrimSpace(parts[1]), "/") {
				root = strings.TrimSpace(parts[1])
			}
			if !strings.HasPrefix(home, "/") || strings.Contains(root, "\x00") {
				entry.State = "error"
				entry.Error = "Invalid WSL home"
			} else {
				entry.Path = `\\wsl.localhost\` + name + strings.ReplaceAll(root, "/", `\`)
			}
		}
		out = append(out, entry)
	}
	return out
}
