package mms

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Carrier is how MMS is reached on one network: an MMS APN, the WAP proxy
// in front of the MMSC, and the MMSC itself (for acknowledgements).
type Carrier struct {
	Name  string
	APN   string
	Proxy string // host:port
	MMSC  string
}

var (
	chinaMobile  = Carrier{Name: "中国移动", APN: "cmwap", Proxy: "10.0.0.172:80", MMSC: "http://mmsc.monternet.com"}
	chinaUnicom  = Carrier{Name: "中国联通", APN: "3gwap", Proxy: "10.0.0.172:80", MMSC: "http://mmsc.myuni.com.cn"}
	chinaTelecom = Carrier{Name: "中国电信", APN: "ctwap", Proxy: "10.0.0.200:80", MMSC: "http://mmsc.vnet.mobi"}
)

// networks maps MCC+MNC to carriers.
var networks = map[string]Carrier{
	"46000": chinaMobile, "46002": chinaMobile, "46004": chinaMobile, "46007": chinaMobile, "46008": chinaMobile, "46013": chinaMobile,
	"46001": chinaUnicom, "46006": chinaUnicom, "46009": chinaUnicom, "46010": chinaUnicom,
	"46003": chinaTelecom, "46005": chinaTelecom, "46011": chinaTelecom, "46012": chinaTelecom,
}

// ErrUnknownCarrier means the SIM's network has no MMS settings here.
var ErrUnknownCarrier = errors.New("无法识别运营商，暂时只支持中国移动、中国联通和中国电信的彩信")

// DetectCarrier picks MMS settings from the SIM's IMSI, or failing that the
// operator the module is registered with (AT+COPS name or MCC+MNC).
func DetectCarrier(imsi, operator string) (Carrier, error) {
	if len(imsi) >= 5 {
		if c, ok := networks[imsi[:5]]; ok {
			return c, nil
		}
		if strings.HasPrefix(imsi, "460") {
			return Carrier{}, fmt.Errorf("%w（IMSI 网络号 %s）", ErrUnknownCarrier, imsi[:5])
		}
	}
	op := strings.ToUpper(strings.TrimSpace(operator))
	if c, ok := networks[op]; ok {
		return c, nil
	}
	switch {
	case strings.Contains(op, "MOBILE") || strings.Contains(op, "CMCC") || strings.Contains(op, "移动"):
		return chinaMobile, nil
	case strings.Contains(op, "UNICOM") || strings.Contains(op, "CUCC") || strings.Contains(op, "联通"):
		return chinaUnicom, nil
	case strings.Contains(op, "TELECOM") || op == "CHN-CT" || strings.Contains(op, "CTCC") || strings.Contains(op, "电信"):
		return chinaTelecom, nil
	}
	return Carrier{}, ErrUnknownCarrier
}

const userAgent = "Android-Mms/2.0"

// GetRequest builds the request that fetches target through the carrier's
// proxy. connect is where the module should connect.
func (c Carrier) GetRequest(target string) (connect string, raw []byte, err error) {
	return c.request("GET", target, nil)
}

// PostRequest builds a request that delivers an MMS PDU to the MMSC.
func (c Carrier) PostRequest(pdu []byte) (connect string, raw []byte, err error) {
	return c.request("POST", c.MMSC, pdu)
}

func (c Carrier) request(method, target string, body []byte) (string, []byte, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" || u.Host == "" || strings.ContainsAny(target, " \r\n\t") {
		return "", nil, fmt.Errorf("不支持的彩信地址：%q", target)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	u.Fragment = ""
	// Through a proxy the request line carries the absolute URL.
	connect, line := target, u.RequestURI()
	if c.Proxy != "" {
		connect, line = "http://"+c.Proxy+"/", u.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\n", method, line)
	fmt.Fprintf(&b, "Host: %s\r\n", u.Host)
	fmt.Fprintf(&b, "User-Agent: %s\r\n", userAgent)
	b.WriteString("Accept: */*, application/vnd.wap.mms-message, application/vnd.wap.sic\r\n")
	b.WriteString("Accept-Language: zh-CN, en-US\r\n")
	if body != nil {
		b.WriteString("Content-Type: application/vnd.wap.mms-message\r\n")
		fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
	}
	b.WriteString("Connection: close\r\n\r\n")
	return connect, append([]byte(b.String()), body...), nil
}
