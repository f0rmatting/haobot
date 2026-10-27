// Package launcher runs the agent in the background for people who start
// it by hand: start remembers the options it was given, prints where the
// admin UI is and the first password, and stop and status find it again
// through a pid file in the data directory.
package launcher

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/transport"
)

// Files in the data directory.
const (
	pidFile  = "haobot.pid"
	logFile  = "haobot.log"
	argsFile = "start.json" // the serve options start was last given
)

//go:embed *.tmpl
var files embed.FS

var rules = template.Must(template.ParseFS(files, "99-haobot.rules.tmpl"))

// UdevRules lets plugdev open and reset every recognised modem on Linux,
// and keeps ModemManager away from them.
func UdevRules() []byte {
	var b bytes.Buffer
	if err := rules.Execute(&b, transport.Models); err != nil {
		panic(err) // the template is fixed
	}
	return b.Bytes()
}

// Launcher starts and stops the agent whose data is in Data.
type Launcher struct {
	Self string // this program
	Data string
	// Listen and Port are serve's defaults, for finding the admin UI.
	Listen string
	Port   int
	Out    io.Writer
}

func (l Launcher) path(name string) string        { return filepath.Join(l.Data, name) }
func (l Launcher) printf(format string, a ...any) { fmt.Fprintf(l.Out, format, a...) }

// Start runs "serve args" in the background, or with no args the options
// it was last started with. -data only says where Data is, so it is
// neither an option to remember nor a reason to forget the others. Start
// returns once the admin UI answers.
func (l Launcher) Start(args []string) error {
	args = withoutFlag(args, "data")
	if pid, ok := l.running(); ok {
		l.printf("号小助已经在运行（进程 %d）。\n", pid)
		l.where(l.savedArgs())
		return nil
	}
	if err := os.MkdirAll(l.Data, 0o700); err != nil {
		return err
	}
	if len(args) == 0 {
		args = l.savedArgs()
	} else if err := l.saveArgs(args); err != nil {
		return err
	}
	serve := append(append([]string{"serve"}, args...), "-data", l.Data)

	log, err := os.OpenFile(l.path(logFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	offset, _ := log.Seek(0, io.SeekEnd)
	fmt.Fprintf(log, "\n---- %s haobot-agent %s\n", time.Now().Format(time.DateTime), strings.Join(serve, " "))
	cmd := exec.Command(l.Self, serve...)
	cmd.Stdout, cmd.Stderr = log, log
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	if err := os.WriteFile(l.path(pidFile), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return err
	}

	_, port := l.address(args)
	for end := time.Now().Add(30 * time.Second); ; time.Sleep(300 * time.Millisecond) {
		select {
		case err := <-exited:
			os.Remove(l.path(pidFile))
			l.printf("号小助没能启动（%v），最后的日志：\n\n%s\n", err, tail(l.readLog(offset), 15))
			return errors.New("启动失败")
		default:
		}
		if listening(port) {
			break
		}
		if time.Now().After(end) {
			l.printf("号小助已在后台启动（进程 %d），但 30 秒内还没开始监听端口 %d，请看日志：%s\n", pid, port, l.path(logFile))
			return nil
		}
	}
	l.printf("✅ 号小助已在后台运行（进程 %d）。\n", pid)
	l.where(args)
	if m := passwordLine.FindSubmatch(l.readLog(offset)); m != nil {
		l.printf("\n  用户名：admin\n  密码：  %s（只显示这一次，可以用 haobot-agent passwd 修改）\n", m[1])
		if l.https(args) {
			l.printf("\n  第一次打开会提示证书不受信任（自签名证书），点“高级 → 继续访问”即可。\n")
		}
	}
	l.printf("  日志：%s\n  停止：haobot-agent stop\n", l.path(logFile))
	return nil
}

// Stop ends the agent start ran, waiting for it to close the database.
func (l Launcher) Stop() error {
	pid, ok := l.running()
	if !ok {
		os.Remove(l.path(pidFile))
		l.printf("号小助没有在后台运行。\n")
		return nil
	}
	if err := terminate(pid); err != nil {
		return err
	}
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if !alive(pid) {
			os.Remove(l.path(pidFile))
			l.printf("号小助已停止。\n")
			return nil
		}
	}
	return fmt.Errorf("进程 %d 15 秒内没有退出，可以用 kill -9 %d 强制结束", pid, pid)
}

// ErrNotRunning is Status's answer when the agent is not running.
var ErrNotRunning = errors.New("not running")

// Status tells whether the agent is running and where.
func (l Launcher) Status() error {
	pid, ok := l.running()
	if !ok {
		l.printf("号小助没有在后台运行。启动：haobot-agent start\n")
		return ErrNotRunning
	}
	l.printf("号小助正在运行（进程 %d）。\n", pid)
	l.where(l.savedArgs())
	l.printf("  日志：%s\n", l.path(logFile))
	return nil
}

// running reads the pid file and checks the process is still this agent.
func (l Launcher) running() (int, bool) {
	data, err := os.ReadFile(l.path(pidFile))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || !alive(pid) {
		return 0, false
	}
	// A stale pid file may name an unrelated process that got the number.
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err == nil && !strings.Contains(string(out), "haobot-agent") {
		return 0, false
	}
	return pid, true
}

func (l Launcher) savedArgs() []string {
	var args []string
	if data, err := os.ReadFile(l.path(argsFile)); err == nil {
		json.Unmarshal(data, &args)
	}
	return withoutFlag(args, "data")
}

func (l Launcher) saveArgs(args []string) error {
	data, _ := json.Marshal(args)
	return os.WriteFile(l.path(argsFile), data, 0o600)
}

func (l Launcher) readLog(offset int64) []byte {
	data, _ := os.ReadFile(l.path(logFile))
	if int64(len(data)) < offset {
		return data
	}
	return data[offset:]
}

// address is where serve with args listens, as serve reads its flags:
// -listen may carry a port, which -port overrides.
func (l Launcher) address(args []string) (string, int) {
	host, port := l.Listen, l.Port
	if v, ok := flagValue(args, "listen"); ok {
		host = v
		if h, p, err := net.SplitHostPort(v); err == nil {
			host = h
			if n, err := strconv.Atoi(p); err == nil {
				port = n
			}
		}
	}
	if v, ok := flagValue(args, "port"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}
	return host, port
}

// where prints the admin UI's addresses.
func (l Launcher) where(args []string) {
	host, port := l.address(args)
	p := strconv.Itoa(port)
	scheme := "https://"
	if !l.https(args) {
		scheme = "http://"
	}
	l.printf("\n  管理后台：%s127.0.0.1:%s\n", scheme, p)
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		l.printf("  （只能在这台机器上打开；局域网访问：haobot-agent stop，再 haobot-agent start -lan）\n")
		return
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			l.printf("            %s%s\n", scheme, net.JoinHostPort(n.IP.String(), p))
		}
	}
}

// https says whether serve with args serves HTTPS, which it does unless
// -tls is turned off.
func (l Launcher) https(args []string) bool {
	v, ok := flagValue(args, "tls")
	if !ok {
		return true
	}
	on, err := strconv.ParseBool(v)
	return err != nil || on
}

// withoutFlag drops -name and its value from args.
func withoutFlag(args []string, name string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		n, _, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if !strings.HasPrefix(args[i], "-") || n != name {
			out = append(out, args[i])
			continue
		}
		if !hasValue {
			i++
		}
	}
	return out
}

// flagValue finds -name value, -name=value or their -- forms in args.
func flagValue(args []string, name string) (string, bool) {
	value, found := "", false
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			continue
		}
		n, v, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if n != name {
			continue
		}
		switch {
		case hasValue:
		case i+1 == len(args) || strings.HasPrefix(args[i+1], "-"):
			v = "true" // a boolean flag on its own
		default:
			i++
			v = args[i]
		}
		value, found = v, true // the last one wins, as with flag
	}
	return value, found
}

// passwordLine finds the password the agent prints on its first start.
var passwordLine = regexp.MustCompile(`已生成 Web 登录密码：(\S+)`)

func listening(port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err == nil {
		c.Close()
	}
	return err == nil
}

// tail returns the last n lines of log.
func tail(log []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(log), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
