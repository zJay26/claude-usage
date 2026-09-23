package usage

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zJay26/claude-usage/internal/sources"
	"os"
	"os/exec"
	"time"
)

type WorkerResult struct {
	Result  ScanResult            `json:"result"`
	Error   string                `json:"error,omitempty"`
	Rebuild *RebuildRequiredError `json:"rebuild,omitempty"`
}

func (s *Scanner) scanWorker(ctx context.Context, home string) (ScanResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return ScanResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "_scan-source", s.Store.DBPath(), home)
	sources.HideCommand(cmd)
	cmd.Env = append(os.Environ(), "CLAUDE_USAGE_SCAN_WORKER=1")
	cmd.WaitDelay = time.Second
	data, err := cmd.Output()
	if err != nil {
		return ScanResult{}, errors.New("WSL transcript scan failed or timed out; existing usage retained")
	}
	var result WorkerResult
	if err = json.Unmarshal(data, &result); err != nil {
		return ScanResult{}, err
	}
	if result.Error != "" {
		if result.Rebuild != nil {
			return result.Result, result.Rebuild
		}
		return result.Result, errors.New(result.Error)
	}
	return result.Result, nil
}
