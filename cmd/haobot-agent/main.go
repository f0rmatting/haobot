// Command haobot-agent (号小助) receives SMS from a cellular modem (Quectel EC20 and
// similar), stores and classifies them in a local SQLite database, and serves
// a password-protected admin UI for messages and the device.
package main

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	// NAS systems and containers may lack /usr/share/zoneinfo; times in the
	// UI and the daily buckets follow TZ.
	_ "time/tzdata"

	"golang.org/x/term"

	"github.com/f0rmatting/haobot/internal/agent/assistant"
	"github.com/f0rmatting/haobot/internal/agent/classify"
	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/notify"
	"github.com/f0rmatting/haobot/internal/agent/pdu"
	"github.com/f0rmatting/haobot/internal/agent/secret"
	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/settings"
	"github.com/f0rmatting/haobot/internal/agent/store"
	"github.com/f0rmatting/haobot/internal/agent/transport"
	"github.com/f0rmatting/haobot/internal/agent/web"
)

const usage = `用法:
  haobot-agent [serve] [参数]       接收短信并启动 Web（默认）
  haobot-agent passwd [参数]        设置 Web 登录密码
  haobot-agent at [参数] <AT指令>…  直接发送 AT 指令（调试用）
  haobot-agent purge-binary [参数]  从数据库删除已保存的二进制短信（彩信通知除外）
  haobot-agent backup [参数]        备份数据库、密钥和通话录音（运行中也可以备份）
  haobot-agent restore [参数] 文件  在新机器上恢复备份（先停止 Agent）
  haobot-agent version              显示版本

运行 "haobot-agent <命令> -h" 查看参数。
`

func main() {
	args := os.Args[1:]
	command := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	if len(args) > 0 && (args[0] == "-version" || args[0] == "--version") {
		command = "version"
	}
	var err error
	switch command {
	case "serve":
		err = runServe(args)
	case "passwd":
		err = runPasswd(args)
	case "at":
		err = runAT(args)
	case "purge-binary":
		err = runPurgeBinary(args)
	case "backup":
		err = runBackup(args)
	case "restore":
		err = runRestore(args)
	case "version":
		fmt.Println("haobot-agent", buildVersion())
	case "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func registerDataDir(fs *flag.FlagSet) *string {
	return fs.String("data", defaultDataDir(), "数据目录（数据库位置）")
}

// portFlags select the modems to use. With none of -device, -usb-location,
// -usb-vid or -usb-pid given, every recognised modem is discovered.
type portFlags struct {
	fs        *flag.FlagSet
	base      transport.Config
	devices   string
	audio     string
	locations string
}

func registerTransport(fs *flag.FlagSet) *portFlags {
	p := &portFlags{fs: fs, base: transport.Config{VendorID: 0x2c7c, ProductID: 0x0125}}
	c := &p.base
	fs.StringVar(&c.Kind, "transport", transport.DefaultKind(), "连接方式：usb（libusb）或 serial")
	fs.StringVar(&p.devices, "device", "", "serial 方式的 AT 串口，多个用逗号分隔，例如 /dev/ttyUSB2,/dev/ttyUSB6；不填则自动发现")
	fs.IntVar(&c.BaudRate, "baud", 115200, "serial 方式的波特率")
	fs.StringVar(&p.audio, "audio-device", "", "serial 方式的通话音频串口，与 -device 一一对应；Linux 上不填会自动查找")
	fs.StringVar(&p.locations, "usb-location", "", "usb 方式的 USB 端口位置，多个用逗号分隔，例如 1-1.2,1-1.3；不填则自动发现")
	fs.Func("usb-vid", "usb 方式的 Vendor ID（默认 0x2c7c）", hexFlag(&c.VendorID))
	fs.Func("usb-pid", "usb 方式的 Product ID（默认 0x0125）", hexFlag(&c.ProductID))
	fs.IntVar(&c.Interface, "usb-interface", 2, "usb 方式的 AT 接口号")
	return p
}

// fixed returns the ports named on the command line, or nil to discover.
func (p *portFlags) fixed() []transport.Config {
	explicit := false
	p.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "device", "audio-device", "usb-location", "usb-vid", "usb-pid", "usb-interface":
			explicit = true
		}
	})
	if !explicit {
		return nil
	}
	var out []transport.Config
	if p.base.Kind == transport.KindSerial {
		audio := splitList(p.audio)
		for i, dev := range splitList(p.devices) {
			cfg := p.base
			cfg.Device = dev
			if i < len(audio) {
				cfg.AudioDevice = audio[i]
			}
			out = append(out, cfg)
		}
		if len(out) == 0 {
			cfg := p.base
			cfg.Device = "/dev/ttyUSB2"
			out = append(out, cfg)
		}
		return out
	}
	for _, loc := range splitList(p.locations) {
		cfg := p.base
		cfg.Location = loc
		out = append(out, cfg)
	}
	if len(out) == 0 {
		out = append(out, p.base)
	}
	return out
}

// one picks a single port for the at command.
func (p *portFlags) one() (transport.Config, error) {
	if fixed := p.fixed(); len(fixed) > 0 {
		return fixed[0], nil
	}
	found, err := transport.Discover(p.base.Kind)
	if err != nil {
		return transport.Config{}, err
	}
	switch len(found) {
	case 0:
		return transport.Config{}, errors.New("没有找到模组；可以用 -device 或 -usb-location 指定")
	case 1:
		return found[0], nil
	}
	var b strings.Builder
	b.WriteString("找到多个模组，请用 -device 或 -usb-location 选择一个：")
	for _, cfg := range found {
		b.WriteString("\n  " + cfg.Describe())
	}
	return transport.Config{}, errors.New(b.String())
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func hexFlag(target *uint16) func(string) error {
	return func(value string) error {
		n, err := strconv.ParseUint(value, 0, 16)
		if err != nil {
			return err
		}
		*target = uint16(n)
		return nil
	}
}

const (
	dbName = "haobot.db"
	// secretKeyName holds the key that encrypts the saved SIM PIN and API key.
	secretKeyName    = "secret.key"
	defaultKeychain  = "haobot-pin"
	currentEnvPrefix = "HAOBOT_"
)

// getenv reads HAOBOT_<name>.
func getenv(name string) string { return os.Getenv(currentEnvPrefix + name) }

func defaultDataDir() string {
	if dir := getenv("DATA"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "data"
	}
	return filepath.Join(home, ".haobot")
}

func openStore(dataDir string) (*store.Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(dataDir, dbName))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dataDir := registerDataDir(fs)
	conn := registerTransport(fs)
	listen := fs.String("listen", cmp.Or(getenv("LISTEN"), "127.0.0.1"), "Web 监听的地址；局域网访问设为 0.0.0.0。也可以连端口一起写，如 0.0.0.0:9081（环境变量 HAOBOT_LISTEN）")
	port := fs.Int("port", defaultPort(), "Web 端口（环境变量 HAOBOT_PORT）")
	storage := fs.String("storage", "ME", "新短信的接收存储：ME（模组）或 SM（SIM 卡）")
	scan := fs.String("scan-storages", "ME,SM", "每次同步读取的存储，逗号分隔")
	deleteAfterSave := fs.Bool("delete-after-save", true, "短信入库后从模组/SIM 卡中删除；-delete-after-save=false 保留")
	keepBinary := fs.Bool("keep-binary", false, "保存二进制短信（彩信通知除外，默认丢弃）")
	poll := fs.Duration("poll", 30*time.Second, "状态刷新与兜底扫描间隔")
	pinFile := fs.String("sim-pin-file", "", "从文件读取 SIM PIN（建议权限 600）")
	pinKeychain := fs.String("sim-pin-keychain", "", "从 macOS 钥匙串读取 SIM PIN 的服务名")
	secureCookie := fs.Bool("secure-cookie", false, "通过 HTTPS 反向代理访问时开启（-tls 开启时自动开启）")
	useTLS := fs.Bool("tls", true, "用 HTTPS 提供 Web，同一端口的 HTTP 请求跳转到 HTTPS；没有 -tls-cert 时自动生成自签名证书。放在反向代理后面时可以用 -tls=false 关闭")
	tlsCert := fs.String("tls-cert", "", "自己的证书文件（PEM），与 -tls-key 一起使用")
	tlsKey := fs.String("tls-key", "", "证书私钥文件（PEM）")
	debug := fs.Bool("debug", false, "输出调试日志")
	debugAudio := fs.Bool("debug-call-audio", false, "把每通电话的原始音频存到数据目录的 audio-debug 下（用于排查通话音质）")
	fs.Parse(args)
	addr, err := listenAddr(*listen, *port, flagSet(fs, "port"))
	if err != nil {
		return err
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := ensurePassword(ctx, st); err != nil {
		return err
	}
	changed, err := st.Redecode(ctx, pdu.Version, func(raw string) (string, bool, error) {
		seg, err := pdu.Decode(raw)
		return seg.Text, seg.Binary, err
	})
	if err != nil {
		return fmt.Errorf("re-decode stored messages: %w", err)
	}
	if changed > 0 {
		log.Info("re-decoded stored messages", "changed", changed, "decoder", pdu.Version)
	}

	box, err := secret.Load(filepath.Join(*dataDir, secretKeyName))
	if err != nil {
		return fmt.Errorf("加载密钥文件：%w", err)
	}
	conf := settings.New(st, box)

	// A PIN given on the command line applies to every device and wins over
	// the ones saved in the UI.
	pin := func(deviceID string) (string, error) { return conf.SIMPIN(ctx, deviceID) }
	if global := pinSource(*pinFile, *pinKeychain); global != nil {
		pin = func(string) (string, error) { return global() }
	}
	var worker *classify.Worker
	recordings := filepath.Join(*dataDir, "recordings")
	client := classify.NewClient()
	notifier := &notify.Notifier{
		Config: conf.Notify, Store: st, Log: log,
		ClassifierReady: func(ctx context.Context) bool {
			cfg, err := conf.LLM(ctx)
			return err == nil && cfg.Ready() && worker.ModelUp()
		},
	}
	aide := &assistant.Agent{Config: conf.Assistant, LLM: conf.LLM, Client: client, Log: log, Answered: st.CountAssistantCalls}
	devices := service.NewManager(service.Options{
		Storage:         strings.ToUpper(*storage),
		ScanStorages:    splitStorages(*scan),
		DeleteAfterSave: *deleteAfterSave,
		KeepBinary:      *keepBinary,
		PollInterval:    *poll,
		PIN:             pin,
		OnMessages: func(msgs []store.Message) {
			worker.Wake()
			notifier.Messages(msgs)
		},
		OnCall:        notifier.Call,
		AudioDebugDir: audioDebugDir(*debugAudio, *dataDir),
		AutoRecover:   func(deviceID string) bool { return conf.AutoRecover(ctx, deviceID) },
		Assistant:     aide,
		RecordingDir:  recordings,
		KeepRecording: func(ctx context.Context, _ string, byAssistant bool) bool {
			cfg, err := conf.Assistant(ctx)
			return err == nil && cfg.ShouldRecord(byAssistant)
		},
	}, conn.fixed(), conn.base.Kind, st, log)
	worker = classify.NewWorker(st, conf.LLM, client, log,
		func() { devices.Hub().Publish(service.TopicMessages) },
		func(level, text string) { devices.Note(ctx, level, text) })
	aide.Note = devices.Note
	notifier.Note = devices.Note
	srv, err := web.New(web.Deps{
		Store: st, Devices: devices, Settings: conf, Worker: worker, Client: client, Assistant: aide,
		RecordingDir: recordings, Notifier: notifier,
		Log: log, SecureCookie: *secureCookie || *useTLS,
	})
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go devices.Run(ctx)
	go pruneRecordings(ctx, st, conf, recordings, log)
	go notifier.Run(ctx)
	go worker.Run(ctx)
	errc := make(chan error, 2)
	scheme, err := serveWeb(httpServer, *useTLS, *tlsCert, *tlsKey, *dataDir, log, errc)
	if err != nil {
		return err
	}
	ports := "自动发现（" + conn.base.Kind + "）"
	if fixed := conn.fixed(); len(fixed) > 0 {
		names := make([]string, len(fixed))
		for i, cfg := range fixed {
			names[i] = cfg.Describe()
		}
		ports = strings.Join(names, ", ")
	}
	log.Info("haobot-agent started", "version", buildVersion(), "web", scheme+"://"+addr, "data", *dataDir, "modems", ports)
	devices.Note(ctx, "info", "号小助已启动")

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}

// serveWeb starts the web server: HTTPS with plain HTTP redirected on the
// same port, or plain HTTP when useTLS is off. It returns the scheme.
func serveWeb(srv *http.Server, useTLS bool, certPath, keyPath, dataDir string, log *slog.Logger, errc chan<- error) (string, error) {
	l, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return "", err
	}
	if !useTLS {
		go func() { errc <- srv.Serve(l) }()
		return "http", nil
	}
	var cert tls.Certificate
	switch {
	case certPath != "" && keyPath != "":
		cert, err = tls.LoadX509KeyPair(certPath, keyPath)
	case certPath != "" || keyPath != "":
		err = web.ErrNoCert
	default:
		var made bool
		cert, made, err = web.SelfSigned(dataDir)
		if made {
			log.Info("generated a self-signed certificate", "dir", dataDir)
		}
		log.Info("self-signed certificate, compare with the browser before trusting it", "sha256", web.Fingerprint(cert))
	}
	if err != nil {
		l.Close()
		return "", fmt.Errorf("TLS 证书：%w", err)
	}
	secure, plain := web.Split(l, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		// HTTP/1.1 only: the call audio WebSocket needs it.
		NextProtos: []string{"http/1.1"},
	})
	redirect := &http.Server{Handler: web.RedirectHTTPS(srv.Addr), ReadHeaderTimeout: 10 * time.Second}
	srv.RegisterOnShutdown(func() { redirect.Close() })
	go func() { errc <- srv.Serve(secure) }()
	go func() {
		if err := redirect.Serve(plain); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	return "https", nil
}

// webPort is the default web port.
const webPort = 9081

func defaultPort() int {
	if p, err := strconv.Atoi(getenv("PORT")); err == nil {
		return p
	}
	return webPort
}

// listenAddr joins the -listen host and the port. A -listen that carries
// its own port (the older form, e.g. 0.0.0.0:8080) keeps it unless -port
// is given too.
func listenAddr(listen string, port int, portSet bool) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("端口 %d 无效，应在 1–65535 之间", port)
	}
	host := listen
	if h, _, err := net.SplitHostPort(listen); err == nil {
		if !portSet {
			return listen, nil
		}
		host = h
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port)), nil
}

func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func splitStorages(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.ToUpper(strings.TrimSpace(part)); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ensurePassword creates the web account with a random password on first start.
func ensurePassword(ctx context.Context, st *store.Store) error {
	exists, err := st.HasUsers(ctx)
	if err != nil || exists {
		return err
	}
	password, err := web.RandomPassword()
	if err != nil {
		return err
	}
	if err := web.SetPassword(ctx, st, password); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\n首次启动，已生成 Web 登录密码：%s\n可用 \"haobot-agent passwd\" 修改。此密码只显示这一次。\n\n", password)
	return nil
}

// pinSource returns a lazy SIM PIN reader, or nil when no source is set.
func pinSource(file, keychainService string) func() (string, error) {
	if pin := getenv("SIM_PIN"); pin != "" {
		return func() (string, error) { return pin, nil }
	}
	switch {
	case file != "":
		return func() (string, error) {
			data, err := os.ReadFile(file)
			return strings.TrimSpace(string(data)), err
		}
	case keychainService != "":
		return func() (string, error) {
			if runtime.GOOS != "darwin" {
				return "", errors.New("-sim-pin-keychain 只支持 macOS")
			}
			out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-w").Output()
			if err != nil {
				return "", fmt.Errorf("读取钥匙串 %q：%w", keychainService, err)
			}
			return strings.TrimSpace(string(out)), nil
		}
	}
	return nil
}

func runPasswd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	dataDir := registerDataDir(fs)
	fs.Parse(args)
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	password, err := readPassword("新密码：")
	if err != nil {
		return err
	}
	confirm, err := readPassword("再输入一次：")
	if err != nil {
		return err
	}
	if password != confirm {
		return errors.New("两次输入的密码不一致")
	}
	if err := web.SetPassword(context.Background(), st, password); err != nil {
		return err
	}
	fmt.Println("密码已更新，已有的登录会话已全部退出。")
	return nil
}

// stdin is shared so a piped password and its confirmation are read from
// the same buffer.
var stdin = bufio.NewReader(os.Stdin)

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func runPurgeBinary(args []string) error {
	fs := flag.NewFlagSet("purge-binary", flag.ExitOnError)
	dataDir := registerDataDir(fs)
	fs.Parse(args)
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := st.DeleteBinaryMessages(context.Background())
	if err != nil {
		return err
	}
	fmt.Printf("已从数据库删除 %d 条二进制短信。\n", n)
	return nil
}

func runAT(args []string) error {
	fs := flag.NewFlagSet("at", flag.ExitOnError)
	conn := registerTransport(fs)
	timeout := fs.Duration("timeout", 10*time.Second, "每条指令的超时")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("请提供至少一条 AT 指令，例如 haobot-agent at 'AT+CSQ'")
	}
	cfg, err := conn.one()
	if err != nil {
		return err
	}
	fmt.Println("模组：", cfg.Describe())
	port, err := transport.Open(cfg)
	if err != nil {
		return err
	}
	session := modem.NewSession(port)
	defer session.Close()
	ctx := context.Background()
	for _, cmd := range fs.Args() {
		fmt.Println(">>", cmd)
		lines, err := session.Command(ctx, cmd, *timeout)
		for _, line := range lines {
			fmt.Println("  ", line)
		}
		var cmdErr *modem.CommandError
		switch {
		case errors.As(err, &cmdErr):
			fmt.Println("  ", cmdErr.Result)
		case err != nil:
			return err
		default:
			fmt.Println("   OK")
		}
	}
	return nil
}

// audioDebugDir creates <data>/audio-debug when call audio recording is on.
func audioDebugDir(on bool, dataDir string) string {
	if !on {
		return ""
	}
	dir := filepath.Join(dataDir, "audio-debug")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}

// pruneRecordings deletes call recordings older than the configured number
// of days, once at start and then every hour.
func pruneRecordings(ctx context.Context, st *store.Store, conf *settings.Settings, dir string, log *slog.Logger) {
	for {
		if cfg, err := conf.Assistant(ctx); err == nil {
			paths, err := st.ExpireRecordings(ctx, time.Now().AddDate(0, 0, -cfg.KeepDays))
			if err != nil {
				log.Warn("expire recordings", "error", err)
			}
			for _, p := range paths {
				os.Remove(filepath.Join(dir, filepath.Clean("/"+p)))
			}
			if len(paths) > 0 {
				log.Info("deleted old call recordings", "count", len(paths))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}
