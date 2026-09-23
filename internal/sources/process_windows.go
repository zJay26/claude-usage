//go:build windows

package sources

import (
	"os/exec"
	"syscall"
)

func HideCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
