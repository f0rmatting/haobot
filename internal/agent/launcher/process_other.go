//go:build !unix

package launcher

import (
	"errors"
	"os/exec"
)

func detach(*exec.Cmd)    {}
func alive(int) bool      { return false }
func groupAlive(int) bool { return false }
func terminate(int) error { return errors.New("这个系统不支持 stop，请直接结束进程") }
