package launcher

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFlagValue(t *testing.T) {
	args := []string{"-transport", "serial", "--listen=0.0.0.0", "-debug", "-port", "9443"}
	if v, ok := flagValue(args, "listen"); !ok || v != "0.0.0.0" {
		t.Errorf("listen = %q %v", v, ok)
	}
	if v, ok := flagValue(args, "port"); !ok || v != "9443" {
		t.Errorf("port = %q %v", v, ok)
	}
	if _, ok := flagValue(args, "data"); ok {
		t.Error("found -data")
	}
}

func TestHTTPSAndWithoutFlag(t *testing.T) {
	var l Launcher
	for args, want := range map[string]bool{"": true, "-tls=false": false, "-tls -port 1": true, "--tls=0": false} {
		if got := l.https(strings.Fields(args)); got != want {
			t.Errorf("%q: https = %v", args, got)
		}
	}
	got := withoutFlag([]string{"-data", "/d", "-lan", "--data=/e", "-port", "1"}, "data")
	if !reflect.DeepEqual(got, []string{"-lan", "-port", "1"}) {
		t.Errorf("got %q", got)
	}
}

func TestAddress(t *testing.T) {
	l := Launcher{Listen: "127.0.0.1", Port: 9081}
	for _, c := range []struct {
		args []string
		host string
		port int
	}{
		{nil, "127.0.0.1", 9081},
		{[]string{"-listen", "0.0.0.0:8443"}, "0.0.0.0", 8443},
		{[]string{"-listen", "0.0.0.0:8443", "-port", "9000"}, "0.0.0.0", 9000},
	} {
		if h, p := l.address(c.args); h != c.host || p != c.port {
			t.Errorf("%q = %s %d", c.args, h, p)
		}
	}
}

func TestUdevRulesCoverModels(t *testing.T) {
	rules := string(UdevRules())
	for _, id := range []string{`"2c7c", ATTR{idProduct}=="0125"`, `"1e0e", ATTR{idProduct}=="9001"`} {
		if !strings.Contains(rules, id) {
			t.Errorf("rules lack %s:\n%s", id, rules)
		}
	}
}

// fakeAgent is a shell script named haobot-agent that, like serve, prints
// the first password and listens on the port in $FAKE_PORT until stopped.
func fakeAgent(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "haobot-agent")
	script := `#!/bin/sh
echo "首次启动，已生成 Web 登录密码：Pw-123" >&2
echo "args: $*"
python3 -c '
import os, socket
s = socket.socket()
s.bind(("127.0.0.1", int(os.environ["FAKE_PORT"])))
s.listen()
while True:
    s.accept()[0].close()
'
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestStartStatusStop(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("needs python3")
	}
	port := freePort(t)
	t.Setenv("FAKE_PORT", fmt.Sprint(port))
	var out bytes.Buffer
	l := Launcher{Self: fakeAgent(t), Data: t.TempDir(), Listen: "127.0.0.1", Port: port, Out: &out}
	t.Cleanup(func() { l.Stop() })

	if err := l.Start([]string{"-poll", "1m"}); err != nil {
		t.Fatalf("start: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Pw-123") || !strings.Contains(out.String(), fmt.Sprintf("https://127.0.0.1:%d", port)) {
		t.Errorf("start said:\n%s", out.String())
	}
	if log, _ := os.ReadFile(filepath.Join(l.Data, logFile)); !strings.Contains(string(log), "args: serve -poll 1m -data "+l.Data) {
		t.Errorf("log:\n%s", log)
	}
	if got := l.savedArgs(); !reflect.DeepEqual(got, []string{"-poll", "1m"}) {
		t.Errorf("saved %q", got)
	}

	out.Reset()
	l.Start(nil)
	if !strings.Contains(out.String(), "已经在运行") {
		t.Errorf("second start said:\n%s", out.String())
	}
	out.Reset()
	l.Status()
	if !strings.Contains(out.String(), "正在运行") {
		t.Errorf("status said:\n%s", out.String())
	}
	out.Reset()
	if err := l.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.running(); ok {
		t.Error("still running")
	}
	out.Reset()
	l.Status()
	if !strings.Contains(out.String(), "没有在后台运行") {
		t.Errorf("status after stop said:\n%s", out.String())
	}

	// -data alone starts with the remembered options.
	if err := l.Start([]string{"-data", l.Data}); err != nil {
		t.Fatal(err)
	}
	if log, _ := os.ReadFile(filepath.Join(l.Data, logFile)); strings.Count(string(log), "args: serve -poll 1m -data "+l.Data) != 2 {
		t.Errorf("restart log:\n%s", log)
	}
}

func TestStartReportsFailure(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "haobot-agent")
	os.WriteFile(bad, []byte("#!/bin/sh\necho '错误: 打不开数据库' >&2\nexit 1\n"), 0o755)
	var out bytes.Buffer
	l := Launcher{Self: bad, Data: t.TempDir(), Listen: "127.0.0.1", Port: freePort(t), Out: &out}
	if err := l.Start(nil); err == nil || !strings.Contains(out.String(), "打不开数据库") {
		t.Errorf("err %v, said:\n%s", err, out.String())
	}
}
