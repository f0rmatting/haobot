// Package classify sorts messages into categories: local rules first, then
// optionally a large language model that only ever sees redacted text.
package classify

import (
	"regexp"
	"strings"
)

// Finding is one kind of sensitive data removed by Redact.
type Finding struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// redaction replaces matches of re. With group > 0 only that submatch is
// replaced, so the surrounding keyword ("验证码：") stays for the model.
type redaction struct {
	kind  string
	re    *regexp.Regexp
	group int
	// keep, when set, vetoes a replacement (e.g. "尊敬的用户" is no name).
	keep func(match string) bool
}

const codeKeywords = `验证码|校验码|动态码|确认码|识别码|激活码|安全码|动态密码|随机码|verification code|security code|code|Code|CODE|OTP`

// codeGap is what may sit between a keyword and the code: punctuation,
// spaces, Chinese words such as "为", or an English "is".
const codeGap = `(?:[^0-9A-Za-z\n]|is){0,12}`

// Separators that may sit between a keyword ("单号") and its value.
const valueGap = `\s*(?:是|为|:|：|#)?\s*`

// redactions run in order; earlier, more specific rules win, and later rules
// skip text already replaced by a placeholder. The last rules are deliberately
// broad: whatever digits survive the specific rules are replaced anyway.
var redactions = []redaction{
	// Verification codes in both orders: "验证码：123456" and "123456是您的验证码".
	{kind: "验证码", re: regexp.MustCompile(`(?:` + codeKeywords + `)` + codeGap + `([0-9A-Za-z]{4,8})\b`), group: 1, keep: hasNoDigit},
	{kind: "验证码", re: regexp.MustCompile(`\b([0-9]{4,8})[^0-9\n]{0,10}(?:` + codeKeywords + `)`), group: 1},
	// Codes that unlock something: lockers, parcels, coupons, gift cards.
	{kind: "取件码", re: regexp.MustCompile(`(?:取件码|提货码|取货码|取餐码|开箱码|提取码|兑换码|券码|核销码|激活码|邀请码|授权码|卡密|密钥)[^0-9A-Za-z\n]{0,6}([0-9A-Za-z][0-9A-Za-z-]{2,31})`), group: 1},
	{kind: "密码", re: regexp.MustCompile(`(?:密码|口令|password|Password|PIN码?|pwd)` + valueGap + `([^\s，。,;；]{4,32})`), group: 1},
	{kind: "身份证号", re: regexp.MustCompile(`\b[1-9]\d{5}(?:18|19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx]\b`)},
	{kind: "证件号", re: regexp.MustCompile(`(?:护照|通行证|证件号码?|证件|驾驶证|社保卡号?|医保卡号?)` + valueGap + `([A-Za-z0-9]{6,20})`), group: 1},
	{kind: "证件号", re: regexp.MustCompile(`\b[EeGgKkDdSsPpHhMm]\d{8}\b`)},
	{kind: "银行卡号", re: regexp.MustCompile(`\b(?:\d{4}[ -]?){3}\d{4,7}\b`)},
	{kind: "银行卡号", re: regexp.MustCompile(`\b\d{4,6}[*＊]{2,}\d{3,4}\b`)},
	{kind: "卡号尾号", re: regexp.MustCompile(`(?:尾号|末四位|后四位|账户|账号|卡号)\s*(?:为|是)?\s*(\d{3,6})`), group: 1},
	{kind: "单号", re: regexp.MustCompile(`(?:订单号?|运单号?|快递单号|物流单号|单号|流水号|交易号|编号|合同号|工单号|保单号|票号|批次号|取票号|电子客票号|预约号|挂号号?|就诊号)` + valueGap + `([0-9A-Za-z][0-9A-Za-z-]{4,31})`), group: 1},
	{kind: "单号", re: regexp.MustCompile(`\b[A-Za-z]{1,4}\d{6,}\b`)},
	{kind: "手机号", re: regexp.MustCompile(`(?:\+?86[ -]?)?\b1[3-9]\d{9}\b`)},
	{kind: "手机号", re: regexp.MustCompile(`\b1[3-9]\d{1,2}[*＊]{3,5}\d{3,4}\b`)},
	{kind: "电话", re: regexp.MustCompile(`\b[48]00[ -]?\d{3}[ -]?\d{4}\b`)},
	{kind: "电话", re: regexp.MustCompile(`(?:\(0\d{2,3}\)|\b0\d{2,3})[ -]?\d{7,8}\b`)},
	{kind: "邮箱", re: regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)},
	{kind: "账号", re: regexp.MustCompile(`(?:微信号?|WeChat|wechat|QQ号?|qq|支付宝账号?|账号|用户名|登录名|会员号)` + valueGap + `([A-Za-z0-9_][A-Za-z0-9_.\-]{4,39})`), group: 1},
	{kind: "车牌", re: regexp.MustCompile(`[京津沪渝冀豫云辽黑湘皖鲁新苏浙赣鄂桂甘晋蒙陕吉闽贵粤青藏川宁琼][A-HJ-NP-Z][·•]?[A-HJ-NP-Z0-9]{4,5}[A-HJ-NP-Z0-9挂学警]`)},
	{kind: "IP", re: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)},
	{kind: "地址", re: regexp.MustCompile(`(?:地址|住址|收货地址|寄件地址|送货地址|送至|配送至|寄往|位于)` + valueGap + `([^\s，。,;；！!？?【】]{4,40})`), group: 1},
	// Administrative names are short ("朝阳区"); a longer prefix would eat
	// the words before the address ("外卖已送达").
	{kind: "地址", re: regexp.MustCompile(`(?:\p{Han}{2,3}(?:省|市))?\p{Han}{2,3}(?:市|区|县|镇|乡)[\p{Han}\d]{0,12}(?:路|街|道|巷|弄|村|里|大道)[\d\-]*号?`)},
	{kind: "门牌", re: regexp.MustCompile(`\d+[-－]?\d*(?:号楼|栋|幢|座|单元|室|号院)(?:\d+[-－]?\d*(?:号楼|栋|幢|座|单元|室|层|号))*`)},
	{kind: "姓名", re: regexp.MustCompile(`(?:姓名|收件人|收货人|寄件人|联系人|乘车人|乘机人|旅客|持卡人|户名|开户名|患者|就诊人|学生|业主)` + valueGap + `(\p{Han}{2,4})`), group: 1, keep: isTitle},
	{kind: "姓名", re: regexp.MustCompile(`(?:尊敬的|亲爱的|您好[,，]?)(\p{Han}{1,3})(?:先生|女士|同学|老师|[,，:：])`), group: 1, keep: isTitle},
	// People addressed inline: "订单E1,张三您已购…", "李四您好", "，王先生".
	{kind: "姓名", re: regexp.MustCompile(`(?:^|[,，。:：;；\s】])(\p{Han}{2,3})(?:您(?:已|的|于|在|好|本次)|你好)`), group: 1, keep: isTitle},
	{kind: "姓名", re: regexp.MustCompile(`(?:^|[,，。:：;；\s】])(\p{Han}{1,2})(?:先生|女士)`), group: 1, keep: isTitle},
	{kind: "姓名", re: regexp.MustCompile(`(?:由|被|交给|转交)(\p{Han}{2,3})(?:签收|代收|领取|取走|收取)`), group: 1, keep: isTitle},
	{kind: "金额", re: regexp.MustCompile(`[¥￥]\s?\d[\d,]*(?:\.\d{1,2})?|\d[\d,]*(?:\.\d{1,2})?\s*(?:元|块钱|万元|美元|港币|欧元|日元)`)},
	// Whatever numbers are left: a year ("2026年") is the only kind kept.
	{kind: "数字", re: regexp.MustCompile(`\d{4,}年?`), keep: isYear},
}

var yearPattern = regexp.MustCompile(`^(?:19|20)\d{2}年$`)

func isYear(match string) bool { return yearPattern.MatchString(match) }

// urlPattern keeps only the host of links: the path often carries tokens.
var urlPattern = regexp.MustCompile(`(?i)\b((?:https?://)?(?:[a-z0-9-]+\.)+(?:com|cn|net|org|cc|me|top|vip|xyz|io|co|info|app|tech|shop|link)(?::\d+)?)(/[^\s，。]*)?`)

func hasNoDigit(match string) bool { return !strings.ContainsAny(match, "0123456789") }

// titles are words in a name position that are not names.
var titles = map[string]bool{
	"用户": true, "客户": true, "会员": true, "旅客": true, "车主": true, "同学": true, "老师": true, "家长": true, "顾客": true, "乘客": true,
	"感谢": true, "欢迎": true, "提醒": true, "邀请": true, "恭喜": true, "通知": true, "告诉": true, "祝福": true, "为了": true, "如果": true, "目前": true,
	"大家": true, "朋友": true, "亲": true, "您": true, "请": true, "已": true, "本人": true, "业主": true, "患者": true, "学生": true, "家人": true,
	"这里": true, "我们": true, "现在": true, "今天": true, "明天": true, "感谢您": true, "女士": true, "先生": true,
	"家属": true, "前台": true, "门卫": true, "快递柜": true, "驿站": true, "物业": true, "他人": true,
}

func isTitle(match string) bool { return titles[match] }

// signaturePattern matches the sender signature that Chinese carriers
// require on service messages, e.g. 【工商银行】 or 【12306】.
var signaturePattern = regexp.MustCompile(`【[^【】\n]{1,20}】`)

// Redact removes personal and secret data from text and reports what it
// found. It is deliberately aggressive: a classifier does not need the data.
// Signatures are kept: they name the company, not the person, and are the
// best hint for the category.
func Redact(text string) (string, []Finding) {
	// The placeholders below borrow private-use characters; drop any the
	// message already contains so they cannot be mistaken for ours.
	text = strings.Map(func(r rune) rune {
		if r >= 0xE000 && r <= 0xF8FF {
			return -1
		}
		return r
	}, text)
	var signatures []string
	text = signaturePattern.ReplaceAllStringFunc(text, func(match string) string {
		signatures = append(signatures, match)
		// A private-use character survives every rule below untouched.
		return string(rune(0xE000 + len(signatures) - 1))
	})
	counts := map[string]int{}
	var order []string
	add := func(kind string) {
		if counts[kind] == 0 {
			order = append(order, kind)
		}
		counts[kind]++
	}
	text = urlPattern.ReplaceAllStringFunc(text, func(match string) string {
		sub := urlPattern.FindStringSubmatch(match)
		if sub[2] == "" || sub[2] == "/" {
			return match
		}
		add("链接参数")
		return sub[1] + "/[链接]"
	})
	for _, r := range redactions {
		placeholder := "[" + r.kind + "]"
		text = r.re.ReplaceAllStringFunc(text, func(match string) string {
			if r.group == 0 {
				if r.keep != nil && r.keep(match) {
					return match
				}
				add(r.kind)
				return placeholder
			}
			loc := r.re.FindStringSubmatchIndex(match)
			if loc == nil || loc[2*r.group] < 0 {
				return match
			}
			start, end := loc[2*r.group], loc[2*r.group+1]
			value := match[start:end]
			if strings.HasPrefix(value, "[") || (r.keep != nil && r.keep(value)) {
				return match
			}
			add(r.kind)
			return match[:start] + placeholder + match[end:]
		})
	}
	for i, sig := range signatures {
		text = strings.Replace(text, string(rune(0xE000+i)), sig, 1)
	}
	findings := make([]Finding, 0, len(order))
	for _, kind := range order {
		findings = append(findings, Finding{Kind: kind, Count: counts[kind]})
	}
	return text, findings
}

// mobileSender matches personal phone numbers, which are redacted before a
// sender is shown to the model: Chinese mobiles in any prefix form, and any
// international number. Service numbers (95588, 106...) are kept.
var mobileSender = regexp.MustCompile(`^(?:(?:(?:\+|00)?86)?1[3-9]\d{9}|(?:\+|00)[1-9]\d{6,14})$`)

// RedactSender hides personal numbers but keeps short service codes, which
// are a strong hint for the category.
func RedactSender(sender string) string {
	if mobileSender.MatchString(sender) {
		return "[个人手机号]"
	}
	return sender
}

// codePattern finds the verification code to offer for copying.
var codePattern = []*regexp.Regexp{
	regexp.MustCompile(`(?:` + codeKeywords + `)` + codeGap + `([0-9]{4,8})\b`),
	regexp.MustCompile(`\b([0-9]{4,8})[^0-9\n]{0,10}(?:` + codeKeywords + `)`),
	regexp.MustCompile(`(?:` + codeKeywords + `)` + codeGap + `([0-9A-Za-z]{4,8})\b`),
}

// ExtractCode returns the verification code in text, or "".
func ExtractCode(text string) string {
	for _, re := range codePattern {
		for _, sub := range re.FindAllStringSubmatch(text, -1) {
			if !hasNoDigit(sub[1]) {
				return sub[1]
			}
		}
	}
	return ""
}
