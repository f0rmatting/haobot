// Package phonebook names the numbers messages and calls come from: the
// user's own contacts first, then built-in yellow pages of companies and
// public services.
package phonebook

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

// Where a name came from.
const (
	SourceContact     = "contact"
	SourceYellowPages = "yellowpages"
)

// Entry is what the phone book knows about a number.
type Entry struct {
	Name     string
	Label    string // which line of an organisation, e.g. 客户回访
	Category string // yellow pages category
	Logo     string // yellow pages logo, a file in Logos
	Source   string
	// Contact is the user's contact the name came from.
	Contact store.Contact
	// Org is the user's change to the yellow pages the name came from.
	Org store.YellowOrg
}

// Key turns a number into the form contacts and yellow pages are indexed
// by: NormalizePeer's, except that a number with +86 and an area code gets
// back the 0 it is dialled with inside China (+86 21-28913888 is
// 02128913888, +86 21 95533 is 02195533).
func Key(number string) string {
	k := store.NormalizePeer(number)
	for _, prefix := range []string{"+86", "0086"} {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		if rest != "" && rest[0] >= '2' && rest[0] <= '9' && !isService(rest) && !strings.HasPrefix(rest, "400") && !strings.HasPrefix(rest, "800") {
			return "0" + rest
		}
		return rest
	}
	return k
}

// isService reports whether n looks like a 95xxx or 96xxx service number,
// possibly with an extension. Landline numbers are 7 or 8 digits, so a
// longer one (9553 3xxx in Shanghai) is not taken for one.
func isService(n string) bool {
	return (strings.HasPrefix(n, "95") || strings.HasPrefix(n, "96")) && len(n) >= 5 && len(n) <= 7
}

// withoutAreaCode returns the service number a key holds after an area
// code: companies call from 95533 with the local area code in front
// (02195533). Area codes are 010, 02x and 0xxx.
func withoutAreaCode(k string) (string, bool) {
	if len(k) < 2 || k[0] != '0' {
		return "", false
	}
	area := 4
	if k[1] == '1' || k[1] == '2' {
		area = 3
	}
	if len(k) <= area || !isService(k[area:]) {
		return "", false
	}
	return k[area:], true
}

//go:embed yellowpages.json
var yellowJSON []byte

//go:embed logos
var logos embed.FS

// Logos holds the yellow pages' logos, PNGs named by their content hash.
var Logos, _ = fs.Sub(logos, "logos")

// YellowPages is the built-in directory, generated from
// https://github.com/metowolf/vCards (MIT) by gen.go.
type YellowPages struct {
	Source string `json:"source"`
	Orgs   []Org  `json:"orgs"`
}

// Org is an organisation of the yellow pages.
type Org struct {
	// ID is the user's change to the organisation (store.YellowOrg), 0 for
	// one as it ships.
	ID int64 `json:"id,omitempty"`
	// Builtin is the name the organisation ships with; "" for the user's own.
	Builtin  string               `json:"builtin,omitempty"`
	Name     string               `json:"name"`
	Category string               `json:"category"`
	Logo     string               `json:"logo,omitempty"` // file in Logos
	Numbers  []store.YellowNumber `json:"numbers"`
	// Edit is the user's change, with whether they gave it a logo.
	Edit store.YellowOrg `json:"-"`
}

// Yellow returns the built-in yellow pages.
var Yellow = sync.OnceValue(func() *YellowPages {
	var y YellowPages
	if err := json.Unmarshal(yellowJSON, &y); err != nil {
		panic("phonebook: bad yellowpages.json: " + err.Error())
	}
	return &y
})

// Pages is the yellow pages with the user's changes.
type Pages struct {
	Source string `json:"source"`
	Orgs   []Org  `json:"orgs"`
	// Hidden are the built-in organisations the user removed.
	Hidden []Org `json:"hidden"`
	index  map[string]Entry
	byName map[string]Entry
}

// merge lays the user's changes over the built-in yellow pages. Changed
// and added organisations are indexed first, so their numbers and names
// win over the built-in ones.
func merge(edits []store.YellowOrg) *Pages {
	y := Yellow()
	p := &Pages{Source: y.Source, Orgs: []Org{}, Hidden: []Org{}, index: map[string]Entry{}, byName: map[string]Entry{}}
	changed := map[string]store.YellowOrg{}
	var own []Org
	for _, e := range edits {
		if e.Builtin != "" {
			changed[e.Builtin] = e
		} else {
			own = append(own, Org{ID: e.ID, Name: e.Name, Category: e.Category, Numbers: e.Numbers, Edit: e})
		}
	}
	for _, o := range y.Orgs {
		o.Builtin = o.Name
		if e, ok := changed[o.Name]; ok {
			o.ID, o.Edit = e.ID, e
			if e.Hidden {
				p.Hidden = append(p.Hidden, o)
				continue
			}
			o.Name, o.Category, o.Numbers = e.Name, e.Category, e.Numbers
		}
		p.Orgs = append(p.Orgs, o)
	}
	p.Orgs = append(p.Orgs, own...)
	for _, edited := range []bool{true, false} {
		for _, o := range p.Orgs {
			if (o.ID != 0) != edited {
				continue
			}
			p.add(o)
		}
	}
	return p
}

func (p *Pages) add(o Org) {
	e := Entry{Name: o.Name, Category: o.Category, Logo: o.Logo, Source: SourceYellowPages, Org: o.Edit}
	for _, n := range o.Numbers {
		k := Key(n.Number)
		if _, dup := p.index[k]; !dup && k != "" {
			e := e
			e.Label = n.Label
			p.index[k] = e
		}
	}
	// A signature may say 【工商银行】 for 中国工商银行.
	name := strings.ToUpper(o.Name)
	names := []string{name}
	if short, ok := strings.CutPrefix(name, "中国"); ok && utf8.RuneCountInString(short) >= 2 {
		names = append(names, short)
	}
	for _, n := range names {
		if _, dup := p.byName[n]; !dup && n != "" {
			p.byName[n] = e
		}
	}
}

var builtin = sync.OnceValue(func() *Pages { return merge(nil) })

// lookup finds a number. 95xxx and 96xxx service numbers are also matched
// by prefix, since companies send from extensions of them (955880 for
// 95588), and without the area code they are called from (02195533),
// unless the yellow pages list that exact number.
func (p *Pages) lookup(k string) (Entry, bool) {
	if e, ok := p.index[k]; ok {
		return e, true
	}
	if strings.HasPrefix(k, "95") || strings.HasPrefix(k, "96") {
		for l := len(k) - 1; l >= 5; l-- {
			if e, ok := p.index[k[:l]]; ok {
				return e, true
			}
		}
	}
	if local, ok := withoutAreaCode(k); ok {
		return p.lookup(local)
	}
	return Entry{}, false
}

// signature matches the 【name】 that Chinese SMS start or end with.
var signature = regexp.MustCompile(`^\s*【([^【】]{2,20})】|【([^【】]{2,20})】\s*$`)

// signed finds the organisation an SMS body is signed by.
func (p *Pages) signed(sender, body string) (Entry, bool) {
	if k := Key(sender); len(k) == 11 && k[0] == '1' {
		return Entry{}, false
	}
	m := signature.FindStringSubmatch(body)
	if m == nil {
		return Entry{}, false
	}
	name := strings.ToUpper(strings.TrimSpace(m[1] + m[2]))
	if e, ok := p.byName[name]; ok {
		return e, true
	}
	if short, ok := strings.CutPrefix(name, "中国"); ok {
		e, ok := p.byName[short]
		return e, ok
	}
	return Entry{}, false
}

// Book looks numbers up in the user's contacts, then the yellow pages with
// the user's changes. Both are kept in memory; call Reload after changing
// them. A nil Book knows only the built-in yellow pages.
type Book struct {
	Store *store.Store

	mu       sync.RWMutex
	contacts map[string]store.Contact
	pages    *Pages
}

// Reload reads the contacts and yellow pages changes from the store.
func (b *Book) Reload(ctx context.Context) error {
	list, err := b.Store.Contacts(ctx)
	if err != nil {
		return err
	}
	edits, err := b.Store.YellowOrgs(ctx)
	if err != nil {
		return err
	}
	contacts := make(map[string]store.Contact, len(list))
	for _, c := range list {
		contacts[c.Key] = c
	}
	pages := merge(edits)
	b.mu.Lock()
	b.contacts, b.pages = contacts, pages
	b.mu.Unlock()
	return nil
}

// Pages returns the yellow pages as the user sees them.
func (b *Book) Pages() *Pages {
	if b == nil {
		return builtin()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.pages == nil {
		return builtin()
	}
	return b.pages
}

// LookupYellow finds a number in the yellow pages.
func (b *Book) LookupYellow(number string) (Entry, bool) {
	return b.Pages().lookup(Key(number))
}

// LookupSignature finds the organisation an SMS body is signed by, as in
// 【招商银行】您的验证码是… Anyone can type a signature, so it is only
// trusted when the sender is not a personal mobile number: carriers let
// only registered signatures through the 106 and 95 gateways.
func (b *Book) LookupSignature(sender, body string) (Entry, bool) {
	return b.Pages().signed(sender, body)
}

// Lookup names a number. A contact saved as 95533 also names 02195533.
// A nil Book knows only the yellow pages.
func (b *Book) Lookup(number string) (Entry, bool) {
	if number == "" {
		return Entry{}, false
	}
	k := Key(number)
	if b != nil {
		b.mu.RLock()
		c, ok := b.contacts[k]
		if !ok {
			if local, isLocal := withoutAreaCode(k); isLocal {
				c, ok = b.contacts[local]
			}
		}
		b.mu.RUnlock()
		if ok {
			return Entry{Name: c.Name, Source: SourceContact, Contact: c}, true
		}
	}
	return b.Pages().lookup(k)
}

// Name is Lookup's name, or "".
func (b *Book) Name(number string) string {
	e, _ := b.Lookup(number)
	return e.Name
}
