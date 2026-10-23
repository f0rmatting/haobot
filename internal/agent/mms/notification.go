// Package mms parses MMS notifications (m-notification-ind) delivered as WAP
// Push over SMS. The notification only announces an MMS; its content stays
// on the carrier's MMSC until fetched over the MMS APN.
package mms

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// ErrNotMMS reports a WAP Push that is not an MMS notification (for example a
// service indication), so callers can keep it as plain binary data.
var ErrNotMMS = errors.New("not an MMS notification")

// Notification is the useful part of an m-notification-ind.
type Notification struct {
	TransactionID string
	From          string
	Subject       string
	Size          int64
	// Expiry is the absolute deadline for downloading, zero if not given.
	Expiry time.Time
	// URL is the Content-Location to fetch from the MMSC.
	URL string
}

const (
	wspPushPDU      = 0x06
	contentTypeMMS  = 0x3E // application/vnd.wap.mms-message (WSP well-known)
	mmsNotification = 0x82 // X-Mms-Message-Type: m-notification-ind
)

// ParseWAPPush decodes the user data of a WAP Push SMS. received anchors a
// relative expiry.
func ParseWAPPush(data []byte, received time.Time) (Notification, error) {
	r := &reader{b: data}
	// Connectionless WSP: transaction id, PDU type, headers length.
	if _, err := r.byte(); err != nil {
		return Notification{}, err
	}
	pduType, err := r.byte()
	if err != nil {
		return Notification{}, err
	}
	if pduType != wspPushPDU {
		return Notification{}, fmt.Errorf("%w: WSP PDU type 0x%02X", ErrNotMMS, pduType)
	}
	headersLen, err := r.uintvar()
	if err != nil {
		return Notification{}, err
	}
	headers, err := r.take(int(headersLen))
	if err != nil {
		return Notification{}, err
	}
	if !isMMSContentType(headers) {
		return Notification{}, ErrNotMMS
	}
	return parseNotification(r.rest(), received)
}

func isMMSContentType(headers []byte) bool {
	if len(headers) == 0 {
		return false
	}
	if headers[0] == 0x80|contentTypeMMS {
		return true
	}
	// Content-general-form or a text string; both contain the media type.
	return bytes.Contains(headers, []byte("application/vnd.wap.mms-message"))
}

func parseNotification(data []byte, received time.Time) (Notification, error) {
	r := &reader{b: data}
	var n Notification
	typeSeen := false
	for !r.done() {
		field, err := r.byte()
		if err != nil {
			return n, err
		}
		if field < 0x80 {
			// Application header (token-text name, then a text value).
			if _, err := r.textString(); err != nil {
				return n, err
			}
			if err := r.skipValue(); err != nil {
				return n, err
			}
			continue
		}
		switch field & 0x7F {
		case 0x0C: // X-Mms-Message-Type
			value, err := r.byte()
			if err != nil {
				return n, err
			}
			if value != mmsNotification {
				return n, fmt.Errorf("%w: message type 0x%02X", ErrNotMMS, value)
			}
			typeSeen = true
		case 0x18: // X-Mms-Transaction-Id
			if n.TransactionID, err = r.textString(); err != nil {
				return n, err
			}
		case 0x09: // From
			if n.From, err = r.from(); err != nil {
				return n, err
			}
		case 0x16: // Subject
			if n.Subject, err = r.encodedString(); err != nil {
				return n, err
			}
		case 0x0E: // X-Mms-Message-Size
			if n.Size, err = r.longInteger(); err != nil {
				return n, err
			}
		case 0x08: // X-Mms-Expiry
			if n.Expiry, err = r.expiry(received); err != nil {
				return n, err
			}
		case 0x03: // X-Mms-Content-Location
			if n.URL, err = r.textString(); err != nil {
				return n, err
			}
		default:
			if err := r.skipValue(); err != nil {
				return n, err
			}
		}
	}
	if !typeSeen {
		return n, fmt.Errorf("%w: missing message type", ErrNotMMS)
	}
	if n.URL == "" {
		return n, errors.New("MMS notification without content location")
	}
	return n, nil
}

// reader walks WSP/MMS encoded values (OMA-WAP-230 8.4.2, OMA-TS-MMS-ENC 7.2).
type reader struct {
	b []byte
	i int
}

var errTruncated = errors.New("truncated MMS notification")

func (r *reader) done() bool   { return r.i >= len(r.b) }
func (r *reader) rest() []byte { return r.b[r.i:] }
func (r *reader) peek() (byte, error) {
	if r.done() {
		return 0, errTruncated
	}
	return r.b[r.i], nil
}

func (r *reader) byte() (byte, error) {
	b, err := r.peek()
	if err == nil {
		r.i++
	}
	return b, err
}

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || r.i+n > len(r.b) {
		return nil, errTruncated
	}
	out := r.b[r.i : r.i+n]
	r.i += n
	return out, nil
}

func (r *reader) uintvar() (uint64, error) {
	var v uint64
	for range 5 {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		v = v<<7 | uint64(b&0x7F)
		if b&0x80 == 0 {
			return v, nil
		}
	}
	return 0, errors.New("uintvar too long")
}

// valueLength reads a Value-length: a short length (0-30) or 31 + uintvar.
func (r *reader) valueLength() (int, error) {
	b, err := r.byte()
	if err != nil {
		return 0, err
	}
	switch {
	case b <= 30:
		return int(b), nil
	case b == 31:
		n, err := r.uintvar()
		return int(n), err
	default:
		return 0, fmt.Errorf("invalid value length 0x%02X", b)
	}
}

func (r *reader) textString() (string, error) {
	end := bytes.IndexByte(r.b[r.i:], 0)
	if end < 0 {
		return "", errTruncated
	}
	raw := r.b[r.i : r.i+end]
	r.i += end + 1
	raw = bytes.TrimPrefix(raw, []byte{0x7F}) // quote for values >= 0x80
	raw = bytes.TrimPrefix(raw, []byte{'"'})  // quoted-string form
	return string(raw), nil
}

func (r *reader) longInteger() (int64, error) {
	n, err := r.byte()
	if err != nil {
		return 0, err
	}
	if n > 30 {
		return 0, fmt.Errorf("invalid long integer length %d", n)
	}
	raw, err := r.take(int(n))
	if err != nil {
		return 0, err
	}
	var v int64
	for _, b := range raw {
		v = v<<8 | int64(b)
	}
	return v, nil
}

// integer reads an Integer-value: a short integer or a long integer.
func (r *reader) integer() (int64, error) {
	b, err := r.peek()
	if err != nil {
		return 0, err
	}
	if b >= 0x80 {
		r.i++
		return int64(b & 0x7F), nil
	}
	return r.longInteger()
}

// encodedString reads an Encoded-string-value, converting it to UTF-8.
func (r *reader) encodedString() (string, error) {
	b, err := r.peek()
	if err != nil {
		return "", err
	}
	if b > 31 {
		text, err := r.textString()
		return toUTF8(text, 106), err
	}
	length, err := r.valueLength()
	if err != nil {
		return "", err
	}
	body, err := r.take(length)
	if err != nil {
		return "", err
	}
	inner := &reader{b: body}
	charset, err := inner.integer()
	if err != nil {
		return "", err
	}
	text, err := inner.textString()
	if err != nil {
		// Some encoders omit the terminating NUL inside the length.
		text = string(bytes.TrimPrefix(inner.rest(), []byte{0x7F}))
	}
	return toUTF8(text, charset), nil
}

func (r *reader) from() (string, error) {
	length, err := r.valueLength()
	if err != nil {
		return "", err
	}
	body, err := r.take(length)
	if err != nil {
		return "", err
	}
	if len(body) == 0 || body[0] != 0x80 { // 0x81 = insert-address-token
		return "", nil
	}
	inner := &reader{b: body[1:]}
	address, err := inner.encodedString()
	if err != nil {
		return "", err
	}
	if i := strings.Index(address, "/TYPE="); i >= 0 {
		address = address[:i]
	}
	return address, nil
}

func (r *reader) expiry(received time.Time) (time.Time, error) {
	length, err := r.valueLength()
	if err != nil {
		return time.Time{}, err
	}
	body, err := r.take(length)
	if err != nil {
		return time.Time{}, err
	}
	inner := &reader{b: body}
	token, err := inner.byte()
	if err != nil {
		return time.Time{}, err
	}
	value, err := inner.longInteger()
	if err != nil {
		return time.Time{}, err
	}
	if token == 0x80 { // absolute, seconds since the epoch
		return time.Unix(value, 0), nil
	}
	return received.Add(time.Duration(value) * time.Second), nil
}

// skipValue skips one header value using the generic WSP value forms.
func (r *reader) skipValue() error {
	b, err := r.peek()
	if err != nil {
		return err
	}
	switch {
	case b <= 31:
		length, err := r.valueLength()
		if err != nil {
			return err
		}
		_, err = r.take(length)
		return err
	case b >= 0x80:
		r.i++
		return nil
	default:
		_, err := r.textString()
		return err
	}
}

// toUTF8 converts text in an IANA MIBenum charset.
func toUTF8(text string, charset int64) string {
	switch charset {
	case 106, 3, 0: // UTF-8, US-ASCII, unspecified
		if utf8.ValidString(text) {
			return text
		}
	case 4: // ISO-8859-1
		runes := make([]rune, 0, len(text))
		for i := 0; i < len(text); i++ {
			runes = append(runes, rune(text[i]))
		}
		return string(runes)
	case 1000, 1013, 1015: // UCS-2 / UTF-16BE / UTF-16
		raw := []byte(text)
		units := make([]uint16, 0, len(raw)/2)
		for i := 0; i+1 < len(raw); i += 2 {
			units = append(units, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
		return string(utf16.Decode(units))
	case 2025, 113, 114: // GB2312, GBK, GB18030
		if decoded, err := simplifiedchinese.GB18030.NewDecoder().String(text); err == nil {
			return decoded
		}
	}
	return strings.ToValidUTF8(text, "�")
}
