package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zJay26/claude-usage/internal/config"
	"github.com/zJay26/claude-usage/internal/updater"
)

// An active installed service owns downloads and survives the CLI returning.
// Checking without a service is read-only with respect to installed binaries.
func (c CLI) updateCommand(args []string) error {
	action := "check"
	if len(args) > 0 {
		action = args[0]
	}
	if (action != "check" && action != "install") || len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
		return fmt.Errorf("usage: claude-usage update [check|install] [--json]")
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return err
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	if !healthOK(base) {
		if action == "install" {
			return fmt.Errorf("请先启动已安装的服务 / Start the installed service before updating")
		}
		manager := newUpdater(paths)
		if err = manager.Check(context.Background()); err != nil {
			return err
		}
		return writePrettyJSON(c.Stdout, manager.Status())
	}
	client := &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{Proxy: nil}}
	call := func(endpoint, body string) (updater.Status, error) {
		var status updater.Status
		request, e := http.NewRequest(http.MethodPost, base+endpoint, strings.NewReader(body))
		if e != nil {
			return status, e
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", base)
		response, e := client.Do(request)
		if e != nil {
			return status, e
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			return status, fmt.Errorf("update: %s", strings.TrimSpace(string(raw)))
		}
		e = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status)
		return status, e
	}
	status, err := call("/api/v1/updates/check", "{}")
	if err != nil {
		return err
	}
	if action == "install" && status.Available {
		raw, _ := json.Marshal(map[string]any{"confirm": true, "version": status.Latest})
		status, err = call("/api/v1/updates/install", string(raw))
		if err != nil {
			return err
		}
	}
	return writePrettyJSON(c.Stdout, status)
}
