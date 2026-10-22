//go:build windows

package codexhost

import (
	"os/exec"
	"syscall"
)

// configureChild hides the console window but keeps normal Windows console
// semantics. Codex's Windows sandbox stalls tool calls in in_progress under
// CREATE_NO_WINDOW, so only HideWindow is set here — the same policy as
// internal/acp/process_start_windows.go.
func configureChild(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
