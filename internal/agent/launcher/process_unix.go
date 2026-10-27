//go:build unix

package launcher

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a session of its own, so closing the terminal does
// not stop it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// terminate asks the agent to stop as Ctrl+C would; detach made it the
// leader of its process group.
func terminate(pid int) error { return syscall.Kill(-pid, syscall.SIGTERM) }
