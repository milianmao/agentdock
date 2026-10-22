//go:build darwin || linux

package codexhost

import (
	"os/exec"

	processcontrol "github.com/uvwt/agentdock/internal/process"
)

// configureChild places the app-server in its own process group so Close can
// terminate the whole tree.
func configureChild(cmd *exec.Cmd) {
	processcontrol.Configure(cmd)
}
