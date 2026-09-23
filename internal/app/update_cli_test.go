package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/zJay26/claude-usage/internal/config"
)

func TestCLIUpdateCheckDoesNotInstallAndInstallSelectsCheckedVersion(t *testing.T) {
	installs := 0
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("Origin") == "" {
			t.Error("unsafe update request")
		}
		if r.URL.Path == "/api/v1/updates/install" {
			var body struct {
				Confirm bool
				Version string
			}
			json.NewDecoder(r.Body).Decode(&body)
			if !body.Confirm || body.Version != "0.2.0" {
				t.Error(body)
			}
			installs++
		}
		w.Write([]byte(`{"available":true,"can_install":true,"latest_version":"0.2.0"}`))
	}))
	defer service.Close()
	t.Setenv("CLAUDE_USAGE_HOME", t.TempDir())
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Port, _ = strconv.Atoi(service.URL[strings.LastIndex(service.URL, ":")+1:])
	if err = config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cli := CLI{Stdout: &out, Stderr: &out}
	if err = cli.updateCommand([]string{"check"}); err != nil || installs != 0 {
		t.Fatal(err, installs)
	}
	if err = cli.updateCommand([]string{"install"}); err != nil || installs != 1 {
		t.Fatal(err, installs)
	}
}
