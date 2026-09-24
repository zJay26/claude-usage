package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentPathsAndReadOnlyClaudeSource(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	home := t.TempDir()
	t.Setenv("CLAUDE_USAGE_HOME", state)
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	sentinel := filepath.Join(home, "settings.json")
	os.WriteFile(sentinel, []byte("{\"sentinel\":true}"), 0600)
	paths, e := ResolvePaths()
	if e != nil {
		t.Fatal(e)
	}
	cfg := Default()
	cfg.AutoDiscoverWSL = false
	if e = Save(paths, cfg); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(paths)
	if e != nil || loaded.Port != 43190 || loaded.ScanIntervalSeconds != 600 {
		t.Fatal(loaded, e)
	}
	homes, e := ClaudeHomes(loaded)
	if e != nil || len(homes) != 1 || homes[0] != home {
		t.Fatal(homes, e)
	}
	data, _ := os.ReadFile(sentinel)
	if string(data) != "{\"sentinel\":true}" {
		t.Fatal("modified Claude config")
	}
}

func TestUnsafeStateAndNonLoopbackRejected(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "personal.txt"), []byte("keep"), 0600)
	t.Setenv("CLAUDE_USAGE_HOME", root)
	if _, e := ResolvePaths(); e == nil {
		t.Fatal("accepted unrelated directory")
	}
	state := filepath.Join(t.TempDir(), "state")
	t.Setenv("CLAUDE_USAGE_HOME", state)
	p, _ := ResolvePaths()
	os.MkdirAll(state, 0700)
	os.WriteFile(p.ConfigPath, []byte(`{"listen_address":"0.0.0.0"}`), 0600)
	if _, e := Load(p); e == nil {
		t.Fatal("accepted remote listener")
	}
}
