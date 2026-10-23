package classify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestRedactRemovesSensitiveData(t *testing.T) {
	cases := []struct {
		in, gone, kind string
	}{
		{"【某银行】您的验证码为 482913，5分钟内有效。", "482913", "验证码"},
		{"728104是您的登录验证码，请勿泄露。", "728104", "验证码"},
		{"Your verification code is 5521. Don't share it.", "5521", "验证码"},
		{"您的初始密码是 Ab12cd!x，请尽快修改。", "Ab12cd!x", "密码"},
		{"身份证 110105199003071234 已完成实名认证", "110105199003071234", "身份证号"},
		{"银行卡 6222 0212 3456 7890 123 已绑定", "6222 0212 3456 7890 123", "银行卡号"},
		{"您尾号8842的储蓄卡支出 120.00 元", "8842", "卡号尾号"},
		{"请联系13812345678确认订单", "13812345678", "手机号"},
		{"尊敬的张三先生，您的预约已确认", "张三", "姓名"},
		{"点击 https://t.example.cn/a?tk=abc123 查看", "tk=abc123", "链接参数"},
		{"【铁路12306】订单E123456789,张三您已购10月25日G101次", "E123456789", "单号"},
		{"【铁路12306】订单E123456789,张三您已购10月25日G101次", "张三", "姓名"},
		{"您账户6688于10月20日收到转账 2,000.00 元", "6688", "卡号尾号"},
		{"快递已到驿站，取件码 3-2-1001，请凭码取件", "3-2-1001", "取件码"},
		{"您的兑换码 XK9-22QP-77，请在 App 内使用", "XK9-22QP-77", "取件码"},
		{"您的护照 E12345678 即将到期", "E12345678", "证件号"},
		{"您的卡 6222****1234 已冻结", "6222****1234", "银行卡号"},
		{"快递单号：SF1234567890123，正在派送", "SF1234567890123", "单号"},
		{"来电 138****5678 已为您转接", "138****5678", "手机号"},
		{"如有疑问请致电 400-820-8820", "400-820-8820", "电话"},
		{"请拨打 010-62345678 联系我们", "010-62345678", "电话"},
		{"添加微信号：zhangsan_1990 领取福利", "zhangsan_1990", "账号"},
		{"您的车辆 京A12345 于今日违停", "京A12345", "车牌"},
		{"账号在 192.168.1.20 登录", "192.168.1.20", "IP"},
		{"收货地址：幸福小区东门3号楼", "幸福小区东门3号楼", "地址"},
		{"包裹已送达北京市朝阳区建国路88号", "建国路88号", "地址"},
		{"请到 12栋2单元301室 签收", "12栋2单元301室", "门牌"},
		{"收件人：王小明，电话已隐藏", "王小明", "姓名"},
		{"李四您好，您的预约已确认", "李四", "姓名"},
		{"，王先生，您的车已保养完成", "王", "姓名"},
		{"您尾号1234的卡支出 ¥3,256.10，余额 128.00元", "3,256.10", "金额"},
		{"您的积分 56789 即将过期", "56789", "数字"},
	}
	for _, c := range cases {
		out, findings := Redact(c.in)
		if strings.Contains(out, c.gone) {
			t.Errorf("Redact(%q) = %q, still contains %q", c.in, out, c.gone)
		}
		found := false
		for _, f := range findings {
			found = found || f.Kind == c.kind
		}
		if !found {
			t.Errorf("Redact(%q) findings %v lack %q", c.in, findings, c.kind)
		}
	}
}

func TestRedactKeepsUsefulContext(t *testing.T) {
	out, _ := Redact("尊敬的客户，您本月账单 356.20 元，最后还款日 10月25日。感谢您的支持，G101 次列车，2026年第一期。退订回T")
	for _, keep := range []string{"客户", "[金额]", "退订", "感谢您", "G101", "2026年", "10月25日", "账单"} {
		if !strings.Contains(out, keep) {
			t.Errorf("redacted %q lost %q", out, keep)
		}
	}
}

func TestExtractCode(t *testing.T) {
	for in, want := range map[string]string{
		"【支付宝】验证码：904721，用于登录":  "904721",
		"328811 is your code":   "328811",
		"您的快递已到驿站，取件请出示手机尾号":    "",
		"登录验证码 A7K9Q2，10 分钟内有效": "A7K9Q2",
	} {
		if got := ExtractCode(in); got != want {
			t.Errorf("ExtractCode(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestClassifySendsOnlyRedactedText checks what actually leaves the machine.
func TestClassifySendsOnlyRedactedText(t *testing.T) {
	var sent string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{
			map[string]any{"message": map[string]string{"content": "```json\n{\"category\": \"账单交易\", \"confidence\": 0.9}\n```"}},
		}})
	}))
	defer api.Close()

	cfg := DefaultConfig()
	cfg.Enabled, cfg.BaseURL, cfg.Model, cfg.APIKey = true, api.URL+"/v1", "test-model", "sk-test"
	result, call, err := NewClient().Classify(context.Background(), cfg, RedactSender("13812345678"), mustRedact("尾号8842 的卡支出 120 元，联系 13900001111"))
	if err != nil {
		t.Fatal(err)
	}
	// The call record shows exactly what was sent and answered.
	if !strings.Contains(sent, call.Prompt[:10]) || call.Reply == "" || call.Model != "test-model" || call.Confidence != 0.9 || !strings.HasSuffix(call.Endpoint, "/v1/chat/completions") {
		t.Fatalf("call = %+v", call)
	}
	if result.Category != Finance {
		t.Fatalf("category = %q", result.Category)
	}
	for _, secret := range []string{"8842", "13900001111", "13812345678"} {
		if strings.Contains(sent, secret) {
			t.Errorf("request to the model contains %q: %s", secret, sent)
		}
	}
}

func TestAnthropicRequest(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "key" || r.Header.Get("anthropic-version") == "" {
			http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]string{"type": "text", "text": `{"category":"营销推广","confidence":0.8}`}}})
	}))
	defer api.Close()
	cfg := DefaultConfig()
	cfg.Enabled, cfg.Provider, cfg.BaseURL, cfg.Model, cfg.APIKey = true, ProviderAnthropic, api.URL, "m", "key"
	result, _, err := NewClient().Classify(context.Background(), cfg, "106", "满减活动")
	if err != nil || result.Category != Marketing {
		t.Fatalf("Classify = %+v, %v", result, err)
	}
}

func TestParseResultUnknownCategoryFallsBack(t *testing.T) {
	r, err := parseResult(`{"category":"火星短信"}`, DefaultConfig())
	if err != nil || r.Category != Other {
		t.Fatalf("parseResult = %+v, %v", r, err)
	}
}

func mustRedact(text string) string {
	out, _ := Redact(text)
	return out
}

// TestRedactCorpus runs typical messages through Redact: no run of four or
// more digits may survive (except a year), and the words the classifier
// needs must.
func TestRedactCorpus(t *testing.T) {
	corpus := []struct{ text, keep string }{
		{"【支付宝】验证码 482913，用于登录，5分钟内有效。", "验证码"},
		{"【工商银行】您尾号8842的储蓄卡10月20日12:31支出（消费）128.00元，余额3,256.10元。", "储蓄卡"},
		{"【菜鸟驿站】您的快递已到达小区东门驿站，取件码 3-2-1001，请凭码取件。", "驿站"},
		{"尊敬的客户，截至10月20日，您本月已用流量 12.3GB，剩余 7.7GB。【中国移动】", "流量"},
		{"【某某商城】双十一预售开启！全场满300减50，会员再享9折，速来抢购 t.cn/abc 退订回T", "退订"},
		{"您好，您有一笔 20 万元低息贷款额度已下发，最快当天到账，详询 4001234567，回T退订", "贷款"},
		{"【铁路12306】订单E123456789,张三您已购10月25日G101次5车8F号，北京南站08:00开。", "【铁路12306】"},
		{"【招商银行】您账户6688于10月20日收到转账 2,000.00 元，摘要：工资。", "转账"},
		{"【顺丰速运】快件 SF1408223346778 已由王五签收，如有疑问请电联 0755-83395566。", "签收"},
		{"【平安保险】尊敬的赵六先生，您的保单 P2026102012345 将于2026年11月1日到期。", "保单"},
		{"【美团】您的外卖已送达朝阳区望京街道阜通东大街6号院3号楼，取餐码 0856。", "外卖"},
		{"【12123】您的车辆 沪B9X8Z7 于10月19日在中山路违停，罚款200元，扣3分。", "【12123】"},
	}
	digits := regexp.MustCompile(`\d{4,}`)
	for _, c := range corpus {
		out, _ := Redact(c.text)
		body := signaturePattern.ReplaceAllString(out, "")
		for _, run := range digits.FindAllString(body, -1) {
			if !strings.Contains(body, run+"年") {
				t.Errorf("Redact(%q) = %q keeps %q", c.text, out, run)
			}
		}
		if !strings.Contains(out, c.keep) {
			t.Errorf("Redact(%q) = %q lost %q", c.text, out, c.keep)
		}
		for _, secret := range []string{"张三", "王五", "赵六", "沪B9X8Z7", "阜通东大街", "8842", "6688"} {
			if strings.Contains(out, secret) {
				t.Errorf("Redact(%q) = %q leaks %q", c.text, out, secret)
			}
		}
	}
}

func TestBailianHost(t *testing.T) {
	for url, want := range map[string]bool{
		"https://dashscope.aliyuncs.com/compatible-mode/v1":                  true,
		"https://ws-abc.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1": true,
		"https://dashscope-intl.aliyuncs.com/compatible-mode/v1":             true,
		"https://api.openai.com/v1":                                          false,
		"https://aliyuncs.com.evil.example/v1":                               false,
	} {
		if got := bailian(url); got != want {
			t.Errorf("bailian(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestRedactSenderHidesPersonalNumbers(t *testing.T) {
	for _, n := range []string{"13812345678", "+8613812345678", "008613812345678", "+14155550199"} {
		if got := RedactSender(n); got != "[个人手机号]" {
			t.Errorf("%s → %s", n, got)
		}
	}
	for _, n := range []string{"95588", "1069012345678901", "10086"} {
		if got := RedactSender(n); got != n {
			t.Errorf("service number %s → %s", n, got)
		}
	}
}
