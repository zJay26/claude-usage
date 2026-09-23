package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/zJay26/claude-usage/internal/pricing"
	"github.com/zJay26/claude-usage/internal/sources"
)

const (
	DefaultPort  = 43191
	databaseName = "usage.sqlite"
)

type Config struct {
	AutoDiscoverWSL     bool                        `json:"auto_discover_wsl"`
	AutoStartWSL        bool                        `json:"auto_start_wsl"`
	ListenAddress       string                      `json:"listen_address"`
	Port                int                         `json:"port"`
	ScanIntervalSeconds int                         `json:"scan_interval_seconds"`
	ExtraClaudeHomes    []string                    `json:"extra_claude_homes,omitempty"`
	PricingOverrides    map[string]pricing.Override `json:"pricing_overrides,omitempty"`
}

type Paths struct {
	StateDir     string
	ConfigPath   string
	Database     string
	BackupDir    string
	InstallDir   string
	InstalledEXE string
}

func Default() Config {
	return Config{
		AutoDiscoverWSL:     runtime.GOOS == "windows",
		AutoStartWSL:        true,
		ListenAddress:       "127.0.0.1",
		Port:                DefaultPort,
		ScanIntervalSeconds: 600,
	}
}

func ResolvePaths() (Paths, error) {
	if override := strings.TrimSpace(os.Getenv("CLAUDE_USAGE_HOME")); override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return Paths{}, err
		}
		if err := validateDedicatedStateDir(abs); err != nil {
			return Paths{}, err
		}
		name := "claude-usage"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return Paths{
			StateDir:     abs,
			ConfigPath:   filepath.Join(abs, "config.json"),
			Database:     filepath.Join(abs, databaseName),
			BackupDir:    filepath.Join(abs, "backups"),
			InstallDir:   filepath.Join(abs, "bin"),
			InstalledEXE: filepath.Join(abs, "bin", name),
		}, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	var stateDir, installDir string
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		stateDir = filepath.Join(base, "claude-usage")
		installDir = filepath.Join(base, "Programs", "claude-usage")
	} else if runtime.GOOS == "darwin" {
		stateDir = filepath.Join(home, "Library", "Application Support", "claude-usage")
		installDir = filepath.Join(stateDir, "bin")
	} else {
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		stateDir = filepath.Join(data, "claude-usage")
		installDir = filepath.Join(home, ".local", "bin")
	}
	if err := validateDedicatedStateDir(stateDir); err != nil {
		return Paths{}, err
	}
	name := "claude-usage"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return Paths{
		StateDir:     stateDir,
		ConfigPath:   filepath.Join(stateDir, "config.json"),
		Database:     filepath.Join(stateDir, databaseName),
		BackupDir:    filepath.Join(stateDir, "backups"),
		InstallDir:   installDir,
		InstalledEXE: filepath.Join(installDir, name),
	}, nil
}

func validateDedicatedStateDir(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	clean := filepath.Clean(absolute)
	root := filepath.Clean(filepath.VolumeName(clean) + string(os.PathSeparator))
	equalPath := func(a, b string) bool {
		if runtime.GOOS == "windows" {
			return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
		}
		return filepath.Clean(a) == filepath.Clean(b)
	}
	if equalPath(clean, root) {
		return fmt.Errorf("CLAUDE_USAGE_HOME 不能是文件系统根目录")
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil && equalPath(clean, home) {
		return fmt.Errorf("CLAUDE_USAGE_HOME 不能是用户主目录")
	}
	entries, readErr := os.ReadDir(clean)
	if errors.Is(readErr, os.ErrNotExist) {
		return nil
	}
	if readErr != nil {
		return readErr
	}
	if len(entries) == 0 {
		return nil
	}
	marker, markerErr := os.ReadFile(filepath.Join(clean, ".claude-usage-state"))
	if markerErr == nil && strings.TrimSpace(string(marker)) == "claude-usage-state-v1" {
		return nil
	}
	managed := map[string]bool{
		"backups": true, "bin": true, "config.json": true, "daemon.log": true,
		databaseName: true, databaseName + "-shm": true, databaseName + "-wal": true,
		databaseName + "-journal": true, "claude-usage.pid": true,
		"claude-usage-start.vbs": true, ".claude-usage-state": true,
	}
	for _, entry := range entries {
		if !managed[entry.Name()] && !strings.HasPrefix(entry.Name(), ".claude-usage-") {
			return fmt.Errorf("%s 不是专用 Claude Usage 状态目录（发现 %q）；请选择新的空目录", clean, entry.Name())
		}
	}
	return nil
}

func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func Load(paths Paths) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(paths.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析 %s: %w", paths.ConfigPath, err)
	}
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = "127.0.0.1"
	}
	if cfg.ListenAddress != "127.0.0.1" && cfg.ListenAddress != "localhost" {
		return Config{}, fmt.Errorf("拒绝非 loopback 监听地址 %q", cfg.ListenAddress)
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("无效端口 %d", cfg.Port)
	}
	if cfg.ScanIntervalSeconds < 600 {
		cfg.ScanIntervalSeconds = 600
	}
	cfg.ExtraClaudeHomes = cleanHomes(cfg.ExtraClaudeHomes)
	cfg.PricingOverrides, err = pricing.NormalizeOverrides(cfg.PricingOverrides)
	if err != nil {
		return Config{}, fmt.Errorf("无效 pricing_overrides: %w", err)
	}
	return cfg, nil
}

func Save(paths Paths, cfg Config) error {
	if err := EnsurePrivateDir(paths.StateDir); err != nil {
		return err
	}
	cfg.ExtraClaudeHomes = cleanHomes(cfg.ExtraClaudeHomes)
	if cfg.ScanIntervalSeconds < 600 {
		cfg.ScanIntervalSeconds = 600
	}
	var err error
	cfg.PricingOverrides, err = pricing.NormalizeOverrides(cfg.PricingOverrides)
	if err != nil {
		return fmt.Errorf("无效 pricing_overrides: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := atomicWrite(paths.ConfigPath, data, 0o600); err != nil {
		return err
	}
	return EnsureStateMarker(paths)
}

func EnsureStateMarker(paths Paths) error {
	if err := EnsurePrivateDir(paths.StateDir); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(paths.StateDir, ".claude-usage-state"),
		[]byte("claude-usage-state-v1\n"), 0o600)
}

func AddHome(paths Paths, raw string) (string, error) {
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s 不是目录", abs)
	}
	cfg, err := Load(paths)
	if err != nil {
		return "", err
	}
	cfg.ExtraClaudeHomes = append(cfg.ExtraClaudeHomes, abs)
	if err := Save(paths, cfg); err != nil {
		return "", err
	}
	return abs, nil
}

func ClaudeHomes(cfg Config) ([]string, error) {
	var homes []string
	if explicit := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return nil, err
		}
		homes = append(homes, abs)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		homes = append(homes, filepath.Join(home, ".claude"))
	}
	homes = append(homes, cfg.ExtraClaudeHomes...)
	return sources.Resolve(cleanHomes(homes), cfg.AutoDiscoverWSL, cfg.AutoStartWSL), nil
}

func cleanHomes(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			continue
		}
		key := filepath.Clean(abs)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, filepath.Clean(abs))
	}
	sort.Strings(out)
	return out
}

func randomSuffix() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "backup"
	}
	return hex.EncodeToString(buf[:])
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude-usage-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(path); err == nil {
			swap := path + ".claude-usage-swap"
			_ = os.Remove(swap)
			if err := os.Rename(path, swap); err != nil {
				return err
			}
			if err := os.Rename(tmpName, path); err != nil {
				_ = os.Rename(swap, path)
				return err
			}
			_ = os.Remove(swap)
			return nil
		}
	}
	return os.Rename(tmpName, path)
}
