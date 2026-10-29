package mms

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Retrieved is an m-retrieve-conf: the MMS itself, as the MMSC returns it.
type Retrieved struct {
	TransactionID string
	From          string
	Subject       string
	Date          time.Time
	// Status is X-Mms-Retrieve-Status: 0x80 (or 0, absent) means OK.
	Status     int
	StatusText string
	Parts      []Part
}

// Part is one body part. Text parts hold UTF-8.
type Part struct {
	ContentType string // lower-case media type without parameters
	Name        string
	Data        []byte
}

const (
	mmsRetrieveConf = 0x84
	maxParts        = 64
	maxDepth        = 2
)

// ErrRetrieveFailed reports an m-retrieve-conf whose status is an error,
// usually because the message expired or was already fetched.
var ErrRetrieveFailed = errors.New("MMSC refused the retrieval")

// ParseRetrieve decodes an m-retrieve-conf.
func ParseRetrieve(data []byte) (Retrieved, error) {
	r := &reader{b: data}
	var m Retrieved
	typeSeen := false
	for !r.done() {
		field, err := r.byte()
		if err != nil {
			return m, err
		}
		if field < 0x80 {
			if _, err := r.textString(); err != nil {
				return m, err
			}
			if err := r.skipValue(); err != nil {
				return m, err
			}
			continue
		}
		switch field & 0x7F {
		case 0x0C: // X-Mms-Message-Type
			value, err := r.byte()
			if err != nil {
				return m, err
			}
			if value != mmsRetrieveConf {
				return m, fmt.Errorf("not an m-retrieve-conf: message type 0x%02X", value)
			}
			typeSeen = true
		case 0x18: // X-Mms-Transaction-Id
			if m.TransactionID, err = r.textString(); err != nil {
				return m, err
			}
		case 0x09: // From
			if m.From, err = r.from(); err != nil {
				return m, err
			}
		case 0x16: // Subject
			if m.Subject, err = r.encodedString(); err != nil {
				return m, err
			}
		case 0x05: // Date
			seconds, err := r.longInteger()
			if err != nil {
				return m, err
			}
			m.Date = time.Unix(seconds, 0)
		case 0x19: // X-Mms-Retrieve-Status
			value, err := r.byte()
			if err != nil {
				return m, err
			}
			m.Status = int(value)
		case 0x1A: // X-Mms-Retrieve-Text
			if m.StatusText, err = r.encodedString(); err != nil {
				return m, err
			}
		case 0x04: // Content-Type, always the last header
			if !typeSeen {
				return m, errors.New("not an m-retrieve-conf: missing message type")
			}
			ct, err := r.contentType()
			if err != nil {
				return m, err
			}
			if m.Status != 0 && m.Status != 0x80 {
				return m, fmt.Errorf("%w: status 0x%02X %s", ErrRetrieveFailed, m.Status, m.StatusText)
			}
			m.Parts, err = parseBody(ct, r.rest(), 0)
			return m, err
		default:
			if err := r.skipValue(); err != nil {
				return m, err
			}
		}
	}
	if typeSeen && m.Status != 0 && m.Status != 0x80 {
		return m, fmt.Errorf("%w: status 0x%02X %s", ErrRetrieveFailed, m.Status, m.StatusText)
	}
	return m, errors.New("m-retrieve-conf without content")
}

type contentType struct {
	media   string
	charset int64
	name    string
}

func (c contentType) multipart() bool {
	return strings.HasPrefix(c.media, "multipart/") || strings.HasPrefix(c.media, "application/vnd.wap.multipart.")
}

func parseBody(ct contentType, data []byte, depth int) ([]Part, error) {
	if !ct.multipart() {
		return []Part{newPart(ct, "", data)}, nil
	}
	if depth >= maxDepth {
		return nil, nil
	}
	r := &reader{b: data}
	count, err := r.uintvar()
	if err != nil {
		return nil, err
	}
	var parts []Part
	for i := uint64(0); i < count && !r.done(); i++ {
		headersLen, err := r.uintvar()
		if err != nil {
			return parts, err
		}
		dataLen, err := r.uintvar()
		if err != nil {
			return parts, err
		}
		headers, err := r.take(int(headersLen))
		if err != nil {
			return parts, err
		}
		body, err := r.take(int(dataLen))
		if err != nil {
			return parts, err
		}
		hr := &reader{b: headers}
		pct, err := hr.contentType()
		if err != nil {
			return parts, err
		}
		location := partName(hr)
		if pct.multipart() {
			nested, err := parseBody(pct, body, depth+1)
			if err != nil {
				return parts, err
			}
			parts = append(parts, nested...)
		} else {
			parts = append(parts, newPart(pct, location, body))
		}
		if len(parts) >= maxParts {
			return parts[:maxParts], nil
		}
	}
	return parts, nil
}

func newPart(ct contentType, location string, data []byte) Part {
	p := Part{ContentType: ct.media, Name: ct.name, Data: data}
	if p.Name == "" {
		p.Name = location
	}
	if strings.HasPrefix(p.ContentType, "text/") && p.ContentType != "text/x-vcard" {
		p.Data = []byte(strings.TrimPrefix(toUTF8(string(data), ct.charset), "\uFEFF"))
	}
	return p
}

// partName reads the part headers after the content type and returns the
// Content-Location, or else the Content-ID.
func partName(r *reader) string {
	var location, id string
	for !r.done() {
		b, err := r.byte()
		if err != nil {
			break
		}
		if b < 0x80 {
			r.i-- // application header: token-text name, text value
			if _, err := r.textString(); err != nil {
				break
			}
			if _, err := r.textString(); err != nil {
				break
			}
			continue
		}
		switch b & 0x7F {
		case 0x0E: // Content-Location
			location, err = r.textString()
		case 0x40: // Content-ID
			id, err = r.textString()
		default:
			err = r.skipValue()
		}
		if err != nil {
			break
		}
	}
	if location != "" {
		return location
	}
	return strings.Trim(id, "<>")
}

// contentType reads a Content-type-value (OMA-WAP-230 8.4.2.24).
func (r *reader) contentType() (contentType, error) {
	b, err := r.peek()
	if err != nil {
		return contentType{}, err
	}
	switch {
	case b >= 0x80:
		r.i++
		return contentType{media: wellKnownMedia(int64(b & 0x7F))}, nil
	case b > 31:
		text, err := r.textString()
		return contentType{media: strings.ToLower(text)}, err
	}
	length, err := r.valueLength()
	if err != nil {
		return contentType{}, err
	}
	body, err := r.take(length)
	if err != nil {
		return contentType{}, err
	}
	inner := &reader{b: body}
	var ct contentType
	if first, err := inner.peek(); err != nil {
		return ct, err
	} else if first > 31 && first < 0x80 {
		text, err := inner.textString()
		if err != nil {
			return ct, err
		}
		ct.media = strings.ToLower(text)
	} else {
		code, err := inner.integer()
		if err != nil {
			return ct, err
		}
		ct.media = wellKnownMedia(code)
	}
	for !inner.done() {
		first, _ := inner.peek()
		if first > 31 && first < 0x80 { // untyped parameter
			name, err := inner.textString()
			if err != nil {
				return ct, nil
			}
			if strings.EqualFold(name, "name") {
				if ct.name, err = inner.textString(); err != nil {
					return ct, nil
				}
				continue
			}
			if inner.skipValue() != nil {
				return ct, nil
			}
			continue
		}
		code, err := inner.integer()
		if err != nil {
			return ct, nil
		}
		switch code {
		case 0x01: // Charset
			if ct.charset, err = inner.integer(); err != nil {
				return ct, nil
			}
		case 0x05, 0x17, 0x06, 0x18: // Name, Filename
			name, err := inner.textString()
			if err != nil {
				return ct, nil
			}
			if ct.name == "" {
				ct.name = name
			}
		default:
			if inner.skipValue() != nil {
				return ct, nil
			}
		}
	}
	return ct, nil
}

// wellKnownMedia maps the WSP content type assignments MMS uses.
func wellKnownMedia(code int64) string {
	switch code {
	case 0x01:
		return "text/*"
	case 0x02:
		return "text/html"
	case 0x03:
		return "text/plain"
	case 0x07:
		return "text/x-vcard"
	case 0x0C:
		return "multipart/mixed"
	case 0x0F:
		return "multipart/alternative"
	case 0x1D:
		return "image/gif"
	case 0x1E:
		return "image/jpeg"
	case 0x1F:
		return "image/tiff"
	case 0x20:
		return "image/png"
	case 0x21:
		return "image/vnd.wap.wbmp"
	case 0x23:
		return "application/vnd.wap.multipart.mixed"
	case 0x26:
		return "application/vnd.wap.multipart.alternative"
	case 0x27:
		return "application/xml"
	case 0x28:
		return "text/xml"
	case 0x33:
		return "application/vnd.wap.multipart.related"
	case 0x3E:
		return "application/vnd.wap.mms-message"
	}
	return "application/octet-stream"
}

// Acknowledge encodes the m-acknowledge-ind that tells the MMSC a
// retrieved message arrived.
func Acknowledge(transactionID string) []byte {
	out := []byte{0x8C, 0x85, 0x98}
	out = append(out, transactionID...)
	return append(out, 0x00, 0x8D, 0x92) // MMS 1.2
}
