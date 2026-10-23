package classify

import "regexp"

// Category is one class a message can be sorted into. The model is given
// Name and Description; Name is what gets stored on the message.
type Category struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Built-in category names that the local rules assign.
const (
	Verification = "验证码"
	Finance      = "账单交易"
	Logistics    = "快递物流"
	Marketing    = "营销推广"
	Telesales    = "推销骚扰"
	Carrier      = "运营商"
	Notice       = "通知提醒"
	Personal     = "个人消息"
	Other        = "其他"
)

// DefaultCategories is the initial, editable category list.
func DefaultCategories() []Category {
	return []Category{
		{Verification, "登录、注册、支付、找回密码等场景的验证码或动态密码"},
		{Finance, "银行、信用卡、支付平台的账单、扣款、到账、还款、余额变动"},
		{Logistics, "快递揽收、派送、取件码、驿站和快递柜通知"},
		{Marketing, "商家和平台的优惠、促销、会员活动、领券，通常带退订提示"},
		{Telesales, "贷款、保险、理财、房产、招聘等推销，以及疑似诈骗、骚扰内容"},
		{Carrier, "运营商的话费、流量、套餐、账单、停复机通知"},
		{Notice, "政务、出行、医疗、预约、物业、服务进度等正常通知"},
		{Personal, "个人之间的普通聊天和往来消息"},
		{Other, "无法归入以上类别的短信"},
	}
}

var verificationRule = regexp.MustCompile(`(?:` + codeKeywords + `)`)

// RuleVerification reports whether text is clearly a verification-code
// message, to offer its code for one-tap copying. Classification itself is
// left to the model.
func RuleVerification(text string) bool {
	return ExtractCode(text) != "" && verificationRule.MatchString(text)
}
