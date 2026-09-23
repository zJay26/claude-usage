package sources

import (
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestWindowsNamesAndDockerIndependentPaths(t *testing.T) {
	var raw []byte
	for _, u := range utf16.Encode([]rune("\ufeffUbuntu\r\n研究🐧\r\ndocker-desktop\r\n")) {
		raw = append(raw, byte(u), byte(u>>8))
	}
	got := names(raw)
	if len(got) != 3 || got[0] != "Ubuntu" || got[1] != "研究🐧" {
		t.Fatal(got)
	}
	for _, p := range []string{`\\wsl.localhost\Ubuntu\home\u\.claude`, `\\WSL$\Ubuntu\home\u\.claude`} {
		if !IsWSLPath(p) {
			t.Fatal(p)
		}
	}
	if IsWSLPath(`C:\Users\demo\.claude`) {
		t.Fatal("native path considered remote")
	}
}

func TestSourceFailureRetainsRetryAndIndependentStatus(t *testing.T) {
	Invalidate()
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	got := Resolve([]string{a, b, a}, false, false)
	if len(got) != 2 {
		t.Fatal(got)
	}
	RecordResult(a, filepath.ErrBadPattern)
	got = Resolve([]string{a, b, a}, false, false)
	if len(got) != 2 {
		t.Fatal("failed source no longer retried")
	}
	statuses := Snapshot()
	if statuses[0].State != "error" || statuses[1].State == "error" {
		t.Fatal(statuses)
	}
	RecordResult(a, nil)
	if Snapshot()[0].Error != "" {
		t.Fatal("stale error")
	}
}
