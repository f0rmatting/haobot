package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/f0rmatting/haobot/internal/agent/launcher"
)

// newLauncher finds the data directory as serve would: -data in args,
// then HAOBOT_DATA, then ~/.haobot.
func newLauncher(args []string) (launcher.Launcher, error) {
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		return launcher.Launcher{}, err
	}
	data := defaultDataDir()
	for i, a := range args {
		if (a == "-data" || a == "--data") && i+1 < len(args) {
			data = args[i+1]
		} else if v, ok := strings.CutPrefix(strings.TrimPrefix(a, "-"), "-data="); ok {
			data = v
		}
	}
	return launcher.Launcher{Self: self, Data: data, Listen: cmp.Or(getenv("LISTEN"), "127.0.0.1"), Port: defaultPort(), Out: os.Stdout}, nil
}

// runStart runs serve in the background. -lan is short for -listen 0.0.0.0.
func runStart(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, `用法: haobot-agent start [serve 的参数]

在后台启动号小助，关掉终端也会继续运行，并显示管理后台地址和首次登录密码。
参数和 serve 相同，另外 -lan 等于 -listen 0.0.0.0（允许局域网访问）。
参数会被记住：以后不带参数运行 start，沿用上一次的参数。

  haobot-agent start           启动（只能在本机打开管理后台）
  haobot-agent start -lan      启动，允许局域网里的电脑、手机打开
  haobot-agent status          查看是否在运行
  haobot-agent stop            停止

日志在数据目录的 haobot.log。用了 -data 的话，stop 和 status 也要带上同样的 -data。
`)
		return nil
	}
	serve := make([]string, 0, len(args))
	for _, a := range args {
		if a == "-lan" || a == "--lan" {
			serve = append(serve, "-listen", "0.0.0.0")
		} else {
			serve = append(serve, a)
		}
	}
	l, err := newLauncher(serve)
	if err != nil {
		return err
	}
	return l.Start(serve)
}

func runStop(args []string) error {
	l, err := newLauncher(args)
	if err != nil {
		return err
	}
	return l.Stop()
}

func runStatus(args []string) error {
	l, err := newLauncher(args)
	if err != nil {
		return err
	}
	if err := l.Status(); errors.Is(err, launcher.ErrNotRunning) {
		os.Exit(3) // as "systemctl status" does for a stopped service
	} else if err != nil {
		return err
	}
	return nil
}

func runUdevRules() error {
	_, err := os.Stdout.Write(launcher.UdevRules())
	return err
}
