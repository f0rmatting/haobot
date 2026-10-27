package phonebook

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/quotedprintable"
	"strings"
	"unicode/utf8"
)

// Card is a person of a vCard file, as much of it as the phone book keeps.
type Card struct {
	Name    string
	Numbers []CardNumber
	Note    string
	Photo   []byte
}

// CardNumber is a TEL of a card, with its type as a label (手机, 工作…).
type CardNumber struct {
	Number string
	Label  string
}

// WriteVCards writes cards as vCard 3.0, which phones and Google Contacts
// all import.
func WriteVCards(w io.Writer, cards []Card) error {
	var b bytes.Buffer
	for _, c := range cards {
		b.WriteString("BEGIN:VCARD\r\nVERSION:3.0\r\n")
		writeLine(&b, "FN:"+escape(c.Name))
		writeLine(&b, "N:"+escape(c.Name)+";;;;")
		for _, n := range c.Numbers {
			writeLine(&b, "TEL;TYPE=VOICE:"+escape(n.Number))
		}
		if c.Note != "" {
			writeLine(&b, "NOTE:"+escape(c.Note))
		}
		if len(c.Photo) > 0 {
			writeLine(&b, "PHOTO;ENCODING=b;TYPE="+photoType(c.Photo)+":"+base64.StdEncoding.EncodeToString(c.Photo))
		}
		b.WriteString("END:VCARD\r\n")
	}
	_, err := w.Write(b.Bytes())
	return err
}

func photoType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG")):
		return "PNG"
	case len(data) > 12 && string(data[8:12]) == "WEBP":
		return "WEBP"
	}
	return "JPEG"
}

// writeLine folds a content line at 75 bytes, between characters.
func writeLine(b *bytes.Buffer, line string) {
	for first := true; ; first = false {
		limit := 75
		if !first {
			b.WriteByte(' ')
			limit = 74
		}
		if len(line) <= limit {
			b.WriteString(line + "\r\n")
			return
		}
		cut := limit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		b.WriteString(line[:cut] + "\r\n")
		line = line[cut:]
	}
}

var escaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, ",", `\,`, ";", `\;`)
var unescaper = strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\:`, ":")

func escape(s string) string   { return escaper.Replace(strings.ReplaceAll(s, "\r\n", "\n")) }
func unescape(s string) string { return unescaper.Replace(s) }

// telLabels names the TEL types people use.
var telLabels = map[string]string{"CELL": "手机", "IPHONE": "手机", "HOME": "家庭", "WORK": "工作", "MAIN": "主要", "FAX": "传真", "PAGER": "寻呼"}

// ReadVCards reads the cards of a vCard file: 2.1 (old Android exports,
// often quoted-printable), 3.0 or 4.0. Cards without a number are left out.
func ReadVCards(data []byte) []Card {
	var cards []Card
	var c *Card
	var family, given, org string
	for _, line := range unfold(string(data)) {
		name, params, value := splitLine(line)
		if enc := params["ENCODING"]; strings.EqualFold(enc, "QUOTED-PRINTABLE") || params["QUOTED-PRINTABLE"] != "" {
			if v, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(value))); err == nil {
				value = string(v)
			}
		}
		switch {
		case name == "BEGIN" && strings.EqualFold(value, "VCARD"):
			c = &Card{}
			family, given, org = "", "", ""
		case c == nil:
		case name == "END":
			if c.Name == "" {
				c.Name = strings.TrimSpace(family + given)
			}
			if c.Name == "" {
				c.Name = org
			}
			if c.Name == "" && len(c.Numbers) > 0 {
				c.Name = c.Numbers[0].Number
			}
			if len(c.Numbers) > 0 {
				cards = append(cards, *c)
			}
			c = nil
		case name == "FN":
			c.Name = strings.TrimSpace(unescape(value))
		case name == "N":
			// Family name first, as Chinese names are written.
			parts := strings.Split(value, ";")
			family = strings.TrimSpace(unescape(parts[0]))
			if len(parts) > 1 {
				given = strings.TrimSpace(unescape(parts[1]))
			}
		case name == "ORG":
			org = strings.TrimSpace(unescape(strings.Split(value, ";")[0]))
		case name == "TEL":
			n := strings.TrimSpace(strings.TrimPrefix(unescape(value), "tel:"))
			if n == "" {
				continue
			}
			label := ""
			for _, t := range strings.Split(strings.ToUpper(params["TYPE"]), ",") {
				if l, ok := telLabels[strings.Trim(t, `"`)]; ok {
					label = l
					break
				}
			}
			c.Numbers = append(c.Numbers, CardNumber{Number: n, Label: label})
		case name == "NOTE":
			c.Note = strings.TrimSpace(unescape(value))
		case name == "PHOTO":
			if _, b64, ok := strings.Cut(value, ";base64,"); ok {
				value = b64 // 4.0: data:image/jpeg;base64,…
			} else if params["ENCODING"] == "" && params["BASE64"] == "" {
				continue // a URL
			}
			if photo, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(value), "")); err == nil {
				c.Photo = photo
			}
		}
	}
	return cards
}

// unfold joins continuation lines (starting with a space or tab), and the
// soft line breaks of quoted-printable values (ending with =).
func unfold(text string) []string {
	var out []string
	qp := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		switch {
		case len(out) > 0 && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")):
			out[len(out)-1] += line[1:]
		case len(out) > 0 && qp:
			out[len(out)-1] += line
		default:
			out = append(out, line)
		}
		last := out[len(out)-1]
		qp = strings.Contains(strings.ToUpper(last), "QUOTED-PRINTABLE") && strings.HasSuffix(last, "=")
	}
	return out
}

// splitLine splits group.NAME;PARAM=a;BARE:value. Bare parameters of
// vCard 2.1 (TEL;CELL:…) count as types.
func splitLine(line string) (name string, params map[string]string, value string) {
	head, value, _ := strings.Cut(line, ":")
	parts := strings.Split(head, ";")
	name = strings.ToUpper(parts[0])
	if _, after, ok := strings.Cut(name, "."); ok {
		name = after
	}
	params = map[string]string{}
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, "=")
		k = strings.ToUpper(k)
		if !ok {
			k, v = "TYPE", k
		}
		if params[k] != "" {
			v = params[k] + "," + v
		}
		params[k] = v
		if k == "TYPE" && (strings.EqualFold(v, "QUOTED-PRINTABLE") || strings.EqualFold(v, "BASE64") || strings.EqualFold(v, "B")) {
			params[strings.ToUpper(v)] = v
		}
	}
	if params["ENCODING"] == "B" || strings.EqualFold(params["ENCODING"], "BASE64") {
		params["BASE64"] = "1"
	}
	return name, params, value
}
