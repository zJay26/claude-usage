//go:build !windows

package sources

import "os/exec"

func HideCommand(cmd *exec.Cmd) {}
