package notify

import (
	"cmp"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"unicode/utf8"

	"github.com/f0rmatting/haobot/internal/agent/classify"
	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

// Span is a piece of a Note's meta line; Code marks a number or code that
// is set apart so it can be copied.
type Span struct {
	Text string
	Code bool
}

// Note is one push message, independent of the channel that renders it.
type Note struct {
	Icon     string
	Title    string
	Tags     []string // shown after the title: category, device
	Meta     []Span   // sender or caller, time
	Headline string   // a one-line gist, shown bold
	Body     string   // the SMS text
	Fields   [][2]string
	Footnote string
	Code     string // a verification code to copy
	Color    string // card colour where supported: blue, green, orange, red, grey
}

// heading is the plain title line used where a separate title is needed.
func (n Note) heading() string {
	return strings.TrimSpace(n.Icon + " " + strings.Join(append([]string{n.Title}, n.Tags...), " · "))
}

func number(n string) string {
	if n == "" {
		return "未知号码"
	}
	return n
}

// MessageNote describes an SMS; a verification code is set apart so it can
// be copied with one tap, or hidden when maskCodes is set.
func MessageNote(m store.Message, device string, maskCodes bool) Note {
	n := Note{Icon: "💬", Title: "短信", Color: "blue"}
	if m.Kind == store.KindMMS {
		n.Icon, n.Title = "🖼", "彩信通知"
	}
	if m.Category != "" {
		n.Tags = append(n.Tags, m.Category)
	}
	if device != "" {
		n.Tags = append(n.Tags, device)
	}
	n.Meta = []Span{{Text: "来自 "}, {Text: number(m.Sender), Code: true}, {Text: " · " + m.ReceivedAt.Format("01-02 15:04")}}
	body := m.Body
	code := classify.ExtractCode(body)
	if maskCodes && code != "" {
		body = strings.ReplaceAll(body, code, strings.Repeat("•", len([]rune(code))))
	}
	if m.Kind == store.KindMMS {
		body = "主题：" + cmp.Or(body, "（无主题）")
	}
	n.Body = body
	if !m.Complete {
		n.Footnote = "（短信不完整，部分分段没有收到）"
	}
	if code != "" && !maskCodes {
		n.Code = code
		n.Color = "green"
	}
	return n
}

// summary mirrors the assistant's note (package assistant).
type summary struct {
	Caller, Purpose, Urgency, Callback, Category, Brief, Error string
}

// CallNote describes a call event.
func CallNote(e service.CallEvent) Note {
	r := e.Record
	var tags []string
	if e.DeviceName != "" {
		tags = []string{e.DeviceName}
	}
	at := " · " + r.StartedAt.Format("01-02 15:04")
	switch e.Kind {
	case service.CallEventRinging:
		return Note{Icon: "📞", Title: "来电", Tags: tags, Color: "orange",
			Meta: []Span{{Text: number(r.Number), Code: true}, {Text: " 正在呼入" + at}}}
	case service.CallEventMissed:
		return Note{Icon: "📵", Title: "未接来电", Tags: tags, Color: "red",
			Meta: []Span{{Text: number(r.Number), Code: true}, {Text: at}}}
	}
	var s summary
	json.Unmarshal([]byte(r.Summary), &s)
	// What the caller said about themselves is all the note knows.
	n := Note{Icon: "🤖", Title: "AI 接听", Tags: tags, Color: "blue", Headline: s.Brief, Footnote: cmp.Or(s.Error, "来电者的身份和说法未经核实"),
		Meta: []Span{{Text: number(r.Number), Code: true}, {Text: at}}}
	if !r.AnsweredAt.IsZero() && !r.EndedAt.IsZero() {
		n.Meta = append(n.Meta, Span{Text: fmt.Sprintf(" · 通话 %d 秒", int(r.EndedAt.Sub(r.AnsweredAt).Seconds()))})
	}
	if s.Urgency == "高" {
		n.Color = "red"
	}
	for _, row := range [][2]string{{"来电者", s.Caller}, {"来意", s.Purpose}, {"紧急程度", s.Urgency}, {"回电", s.Callback}, {"类别", s.Category}} {
		if row[1] != "" {
			n.Fields = append(n.Fields, row)
		}
	}
	return n
}

// TestNote is sent by the settings page's test button.
func TestNote(kind Kind) Note {
	return Note{Icon: "✅", Title: "号小助测试消息", Color: "green",
		Body: "收到这条消息，说明新短信、来电和 AI 接听小结会推送到" + kind.Label() + "。"}
}

// ---- Telegram HTML ----

var esc = html.EscapeString

// HTML renders a note for Telegram's HTML parse mode, within its length
// limit. The body is shortened before escaping: cutting the escaped text
// could split an entity or a tag, and Telegram rejects such a message.
func (n Note) HTML() string {
	out := n.html()
	for body := []rune(n.Body); utf8.RuneCountInString(out) > maxMessage && len(body) > 0; {
		body = body[:len(body)*3/4]
		n.Body = string(body) + "…"
		out = n.html()
	}
	return out
}

func (n Note) html() string {
	var b strings.Builder
	b.WriteString(n.Icon + " <b>" + esc(n.Title) + "</b>")
	for _, t := range n.Tags {
		b.WriteString(" · " + esc(t))
	}
	if len(n.Meta) > 0 {
		b.WriteString("\n")
		for _, s := range n.Meta {
			if s.Code {
				b.WriteString("<code>" + esc(s.Text) + "</code>")
			} else {
				b.WriteString(esc(s.Text))
			}
		}
	}
	if n.Headline != "" {
		b.WriteString("\n\n<b>" + esc(n.Headline) + "</b>")
	}
	if n.Body != "" {
		b.WriteString("\n\n" + esc(n.Body))
	}
	for i, f := range n.Fields {
		if i == 0 && n.Headline == "" && n.Body == "" {
			b.WriteString("\n")
		}
		b.WriteString("\n" + f[0] + "：" + esc(f[1]))
	}
	if n.Footnote != "" {
		b.WriteString("\n<i>" + esc(n.Footnote) + "</i>")
	}
	if n.Code != "" {
		b.WriteString("\n\n验证码 <code>" + esc(n.Code) + "</code>")
	}
	return b.String()
}

// ---- Markdown (飞书, 钉钉, 企业微信) ----

// BotKeyword ends every group-bot message, so it can be the bot's custom
// keyword.
const BotKeyword = "号小助"

// mdSafe keeps message text from being read as markup by swapping the
// characters these dialects use for full-width look-alikes.
var mdSafe = strings.NewReplacer(
	"*", "＊", "`", "ˋ", "_", "＿", "[", "［", "]", "］",
	"<", "＜", ">", "＞", "#", "＃", "~", "～", "|", "｜",
)

// markdown renders a note body for the markdown dialects; newline is the
// line break the dialect needs, and code formats a span set apart.
func (n Note) markdown(newline string, code func(string) string) string {
	var lines []string
	if len(n.Meta) > 0 {
		var b strings.Builder
		for _, s := range n.Meta {
			if s.Code {
				b.WriteString(code(mdSafe.Replace(s.Text)))
			} else {
				b.WriteString(mdSafe.Replace(s.Text))
			}
		}
		lines = append(lines, b.String())
	}
	if n.Headline != "" {
		lines = append(lines, "", "**"+mdSafe.Replace(n.Headline)+"**")
	}
	if n.Body != "" {
		lines = append(lines, "")
		for _, l := range strings.Split(n.Body, "\n") {
			lines = append(lines, mdSafe.Replace(l))
		}
	}
	if len(n.Fields) > 0 {
		lines = append(lines, "")
		for _, f := range n.Fields {
			lines = append(lines, f[0]+"："+mdSafe.Replace(f[1]))
		}
	}
	if n.Footnote != "" {
		lines = append(lines, mdSafe.Replace(n.Footnote))
	}
	if n.Code != "" {
		lines = append(lines, "", "验证码 "+code(n.Code))
	}
	// Bots set up with a custom keyword only take messages that contain it;
	// the setup guide suggests this one.
	lines = append(lines, "", "— "+BotKeyword)
	return strings.Join(lines, newline)
}
