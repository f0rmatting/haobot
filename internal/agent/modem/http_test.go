package modem

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// scriptedModem hands every command to answer, which writes the reply to
// the device end itself and may read raw input from it.
func scriptedModem(t *testing.T, answer func(cmd string, r *bufio.Reader, w io.Writer)) *Modem {
	t.Helper()
	host, device := net.Pipe()
	go func() {
		reader := bufio.NewReader(device)
		for {
			cmd, err := reader.ReadString('\r')
			if err != nil {
				return
			}
			answer(strings.TrimSuffix(cmd, "\r"), reader, device)
		}
	}()
	s := NewSession(host)
	t.Cleanup(func() { s.Close(); device.Close() })
	return New(s)
}

func TestHTTPGetAndReadFile(t *testing.T) {
	body := []byte("\x8C\x84\r\nOK\r\n\x00binary") // looks like result lines on purpose
	var gotURL, gotRequest string
	m := scriptedModem(t, func(cmd string, r *bufio.Reader, w io.Writer) {
		readInput := func(n int) string {
			io.WriteString(w, "\r\nCONNECT\r\n")
			buf := make([]byte, n)
			io.ReadFull(r, buf)
			io.WriteString(w, "\r\nOK\r\n")
			return string(buf)
		}
		switch {
		case strings.HasPrefix(cmd, "AT+QHTTPCFG="):
			io.WriteString(w, "\r\nOK\r\n")
		case strings.HasPrefix(cmd, "AT+QHTTPURL="):
			n, _ := strconv.Atoi(strings.Split(strings.TrimPrefix(cmd, "AT+QHTTPURL="), ",")[0])
			gotURL = readInput(n)
		case strings.HasPrefix(cmd, "AT+QHTTPGET="):
			n, _ := strconv.Atoi(strings.Split(cmd, ",")[1])
			gotRequest = readInput(n)
			io.WriteString(w, "\r\n+QHTTPGET: 0,200,"+strconv.Itoa(len(body))+"\r\n")
		case strings.HasPrefix(cmd, "AT+QFOPEN="):
			io.WriteString(w, "\r\n+QFOPEN: 7\r\nOK\r\n")
		case cmd == "AT+QFREAD=7,4096":
			io.WriteString(w, "\r\nCONNECT "+strconv.Itoa(len(body))+"\r\n")
			w.Write(body)
			io.WriteString(w, "\r\nOK\r\n")
		case cmd == "AT+QFCLOSE=7":
			io.WriteString(w, "\r\nOK\r\n")
		default:
			io.WriteString(w, "\r\nERROR\r\n")
		}
	})
	ctx := context.Background()
	p, err := m.StartHTTP(ctx, HTTPRequest{Context: 3, Connect: "http://10.0.0.172:80/", Raw: []byte("GET / HTTP/1.1\r\n\r\n"), Wait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	value, err := p.Wait(ctx, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := ParseHTTPResult(value); err != nil || status != 200 {
		t.Fatalf("status = %d, %v", status, err)
	}
	if gotURL != "http://10.0.0.172:80/" || gotRequest != "GET / HTTP/1.1\r\n\r\n" {
		t.Fatalf("url %q request %q", gotURL, gotRequest)
	}

	handle, err := m.OpenFile(ctx, "UFS:x.bin")
	if err != nil || handle != 7 {
		t.Fatalf("handle = %d, %v", handle, err)
	}
	block, err := m.ReadChunk(ctx, handle)
	if err != nil || string(block) != string(body) {
		t.Fatalf("block = %q, %v", block, err)
	}
	if err := m.CloseFile(ctx, handle); err != nil {
		t.Fatal(err)
	}
	// The session is still in step after the raw data.
	if _, err := m.s.Command(ctx, "AT+QHTTPCFG=\"x\"", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestExpectClaimsResultDuringOtherCommand(t *testing.T) {
	m := scriptedModem(t, func(cmd string, r *bufio.Reader, w io.Writer) {
		// The late result arrives in the middle of another command's reply.
		io.WriteString(w, "\r\n+QHTTPREADFILE: 0\r\n+CSQ: 20,99\r\nOK\r\n")
	})
	p := m.expect("+QHTTPREADFILE:")
	lines, err := m.s.Command(context.Background(), "AT+CSQ", time.Second)
	if err != nil || len(lines) != 1 || lines[0] != "+CSQ: 20,99" {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	if value, err := p.Wait(context.Background(), time.Second); err != nil || value != "0" {
		t.Fatalf("value = %q, %v", value, err)
	}
}

func TestParseHTTPResultError(t *testing.T) {
	_, err := ParseHTTPResult("714")
	if err == nil || !strings.Contains(err.Error(), "域名解析失败") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenContextPicksFreeContext(t *testing.T) {
	var configured string
	m := scriptedModem(t, func(cmd string, r *bufio.Reader, w io.Writer) {
		switch {
		case cmd == "AT+CGDCONT?":
			io.WriteString(w, "\r\n+CGDCONT: 1,\"IPV4V6\",\"3gnet\",\"0.0.0.0\",0,0\r\n+CGDCONT: 2,\"IPV4V6\",\"ims\",\"0.0.0.0\",0,0\r\nOK\r\n")
		case strings.HasPrefix(cmd, "AT+QICSGP="):
			configured = cmd
			io.WriteString(w, "\r\nOK\r\n")
		case cmd == "AT+QIACT?":
			io.WriteString(w, "\r\n+QIACT: 1,1,1,\"10.1.2.3\"\r\nOK\r\n")
		case cmd == "AT+QIACT=3":
			io.WriteString(w, "\r\nOK\r\n")
		default:
			io.WriteString(w, "\r\nERROR\r\n")
		}
	})
	cid, opened, err := m.OpenContext(context.Background(), "3gwap")
	if err != nil || cid != 3 || !opened {
		t.Fatalf("cid %d opened %v err %v", cid, opened, err)
	}
	if configured != `AT+QICSGP=3,1,"3gwap","","",0` {
		t.Fatalf("configured %q", configured)
	}
}

func TestOpenContextReusesActive(t *testing.T) {
	m := scriptedModem(t, func(cmd string, r *bufio.Reader, w io.Writer) {
		switch cmd {
		case "AT+CGDCONT?":
			io.WriteString(w, "\r\n+CGDCONT: 1,\"IP\",\"CMWAP\",\"0.0.0.0\",0,0\r\nOK\r\n")
		case "AT+QIACT?":
			io.WriteString(w, "\r\n+QIACT: 1,1,1,\"10.1.2.3\"\r\nOK\r\n")
		default:
			io.WriteString(w, "\r\nERROR\r\n")
		}
	})
	cid, opened, err := m.OpenContext(context.Background(), "cmwap")
	if err != nil || cid != 1 || opened {
		t.Fatalf("cid %d opened %v err %v", cid, opened, err)
	}
}
