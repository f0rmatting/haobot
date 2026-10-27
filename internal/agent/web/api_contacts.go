package web

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/f0rmatting/haobot/internal/agent/phonebook"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

// maxAvatar bounds a contact picture; the UI sends a small JPEG, and the
// base64 of it must fit in a request body (maxBody).
const maxAvatar = 40 << 10

// caller is the phone book's name and picture for a row's number.
type caller struct {
	Name       string `json:"name,omitempty"`
	NameSource string `json:"nameSource,omitempty"` // contact or yellowpages
	Avatar     string `json:"avatar,omitempty"`     // picture URL
}

// A contact without a photo shows the yellow pages logo of its number.
func (s *Server) caller(number string) caller {
	e, _ := s.Phonebook.Lookup(number)
	c := caller{e.Name, e.Source, avatarURL(e)}
	if c.Avatar == "" && e.Source == phonebook.SourceContact {
		if y, ok := s.Phonebook.LookupYellow(number); ok {
			c.Avatar = avatarURL(y)
		}
	}
	return c
}

// messageCaller is caller for an SMS's sender; a sender without a picture
// gets the logo of the organisation the text is signed by (【招商银行】).
func (s *Server) messageCaller(m store.Message) caller {
	c := s.caller(m.Peer)
	if c.Avatar == "" && m.Direction == store.DirectionIn && m.Kind == store.KindSMS && !m.Binary {
		if e, ok := s.Phonebook.LookupSignature(m.Sender, m.Body); ok {
			c.Avatar = avatarURL(e)
		}
	}
	return c
}

func avatarURL(e phonebook.Entry) string {
	switch {
	case e.Source == phonebook.SourceContact && e.Contact.HasAvatar:
		return contactAvatarURL(e.Contact)
	case e.Org.HasLogo:
		return orgLogoURL(e.Org)
	case e.Logo != "":
		return "/api/yellowpages/logos/" + e.Logo
	}
	return ""
}

// contactAvatarURL changes with the contact, so browsers may cache it.
func contactAvatarURL(c store.Contact) string {
	return "/api/contacts/" + strconv.FormatInt(c.ID, 10) + "/avatar?v=" + strconv.FormatInt(c.UpdatedAt.Unix(), 10)
}

type contactJSON struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Number    string    `json:"number"`
	Note      string    `json:"note"`
	Avatar    string    `json:"avatar,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// toContactJSON gives a contact without a photo its number's yellow pages
// logo, as caller does.
func (s *Server) toContactJSON(c store.Contact) contactJSON {
	j := contactJSON{ID: c.ID, Name: c.Name, Number: c.Number, Note: c.Note, UpdatedAt: c.UpdatedAt}
	if c.HasAvatar {
		j.Avatar = contactAvatarURL(c)
	} else if y, ok := s.Phonebook.LookupYellow(c.Number); ok {
		j.Avatar = avatarURL(y)
	}
	return j
}

func (s *Server) routeContacts(authed func(string, http.HandlerFunc)) {
	authed("GET /api/contacts", s.listContacts)
	authed("POST /api/contacts", s.saveContact)
	authed("PUT /api/contacts/{id}", s.saveContact)
	authed("DELETE /api/contacts/{id}", s.deleteContact)
	authed("GET /api/contacts/{id}/avatar", s.contactAvatar)
	authed("GET /api/contacts/export", s.exportContacts)
	authed("POST /api/contacts/import", s.importContacts)
	s.routeYellow(authed)
}

func (s *Server) listContacts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Contacts(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	items := make([]contactJSON, 0, len(list))
	for _, c := range list {
		items = append(items, s.toContactJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) saveContact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Number string `json:"number"`
		Note   string `json:"note"`
		// Avatar is a data: URL of a JPEG, PNG or WebP; "" removes the
		// picture and leaving it out keeps it.
		Avatar *string `json:"avatar"`
	}
	if !decode(w, r, &req) {
		return
	}
	var avatar []byte
	if req.Avatar != nil && *req.Avatar != "" {
		var err error
		if avatar, err = decodeAvatar(*req.Avatar); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	c := store.Contact{Name: strings.TrimSpace(req.Name), Number: strings.TrimSpace(req.Number), Note: strings.TrimSpace(req.Note)}
	c.Key = phonebook.Key(c.Number)
	switch {
	case c.Name == "":
		writeError(w, http.StatusBadRequest, "请填写名字")
		return
	case c.Key == "":
		writeError(w, http.StatusBadRequest, "请填写号码")
		return
	case utf8.RuneCountInString(c.Name) > 64 || len(c.Number) > 32 || utf8.RuneCountInString(c.Note) > 500:
		writeError(w, http.StatusBadRequest, "内容太长")
		return
	}
	if id := r.PathValue("id"); id != "" {
		var err error
		if c.ID, err = strconv.ParseInt(id, 10, 64); err != nil {
			writeError(w, http.StatusBadRequest, "无效的联系人")
			return
		}
	}
	c, err := s.Store.SaveContact(r.Context(), c)
	switch {
	case errors.Is(err, store.ErrContactExists):
		writeError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "联系人不存在")
		return
	case err != nil:
		s.serverError(w, err)
		return
	}
	if req.Avatar != nil {
		if err := s.Store.SetContactAvatar(r.Context(), c.ID, avatar); err != nil {
			s.serverError(w, err)
			return
		}
	}
	if err := s.Phonebook.Reload(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	// The reloaded book has the contact as stored, with its picture state.
	saved, _ := s.Phonebook.Lookup(c.Number)
	if saved.Contact.ID == c.ID {
		c = saved.Contact
	}
	writeJSON(w, http.StatusOK, s.toContactJSON(c))
}

var avatarTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// decodeAvatar reads a picture from a data: URL.
func decodeAvatar(dataURL string) ([]byte, error) {
	_, b64, ok := strings.Cut(dataURL, ";base64,")
	if !ok || !strings.HasPrefix(dataURL, "data:image/") {
		return nil, errors.New("头像格式不对")
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	switch {
	case err != nil:
		return nil, errors.New("头像格式不对")
	case len(data) > maxAvatar:
		return nil, errors.New("头像图片太大")
	case !avatarTypes[http.DetectContentType(data)]:
		return nil, errors.New("头像只支持 JPEG、PNG 或 WebP 图片")
	}
	return data, nil
}

func (s *Server) contactAvatar(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的联系人")
		return
	}
	data, err := s.Store.ContactAvatar(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "没有头像")
		return
	} else if err != nil {
		s.serverError(w, err)
		return
	}
	// The URL carries the contact's version (contactAvatarURL).
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Write(data)
}

func (s *Server) deleteContact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的联系人")
		return
	}
	if err := s.Store.DeleteContact(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "联系人不存在")
		return
	} else if err != nil {
		s.serverError(w, err)
		return
	}
	if err := s.Phonebook.Reload(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	ok(w)
}

// maxImport bounds an imported address book or yellow pages file, photos
// and logos included.
const maxImport = 16 << 20

func (s *Server) exportContacts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Contacts(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	cards := make([]phonebook.Card, 0, len(list))
	for _, c := range list {
		card := phonebook.Card{Name: c.Name, Numbers: []phonebook.CardNumber{{Number: c.Number}}, Note: c.Note}
		if c.HasAvatar {
			if card.Photo, err = s.Store.ContactAvatar(r.Context(), c.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.serverError(w, err)
				return
			}
		}
		cards = append(cards, card)
	}
	w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
	attachment(w, "号小助联系人-"+time.Now().Format("20060102")+".vcf")
	phonebook.WriteVCards(w, cards)
}

// attachment makes the browser save the response as a file.
func attachment(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Cache-Control", "no-store")
}

// importResult counts what an import did.
type importResult struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"` // already there, or unusable
}

// importContacts adds the people of a vCard file. A card with several
// numbers becomes a contact per number, named after the number's type. A
// number already in the contacts is skipped, or overwritten if asked.
func (s *Server) importContacts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VCF       string `json:"vcf"`
		Overwrite bool   `json:"overwrite"`
	}
	if !decodeLimit(w, r, &req, maxImport) {
		return
	}
	cards := phonebook.ReadVCards([]byte(req.VCF))
	if len(cards) == 0 {
		writeError(w, http.StatusBadRequest, "文件里没有找到带号码的联系人，请选择 vCard（.vcf）文件")
		return
	}
	list, err := s.Store.Contacts(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	existing := make(map[string]store.Contact, len(list))
	for _, c := range list {
		existing[c.Key] = c
	}
	var res importResult
	for _, card := range cards {
		photo := fitAvatar(card.Photo)
		for i, n := range card.Numbers {
			c := store.Contact{Name: clip(card.Name, 64), Number: strings.TrimSpace(n.Number), Note: clip(card.Note, 500)}
			if len(card.Numbers) > 1 {
				label := n.Label
				if label == "" {
					label = strconv.Itoa(i + 1)
				}
				c.Name = clip(card.Name, 60) + " " + label
			}
			c.Key = phonebook.Key(c.Number)
			old, there := existing[c.Key]
			switch {
			case c.Key == "" || len(c.Number) > 32:
				res.Skipped++
				continue
			case there && !req.Overwrite:
				res.Skipped++
				continue
			case there:
				c.ID = old.ID
			}
			saved, err := s.Store.SaveContact(r.Context(), c)
			if err != nil {
				s.serverError(w, err)
				return
			}
			if photo != nil {
				if err := s.Store.SetContactAvatar(r.Context(), saved.ID, photo); err != nil {
					s.serverError(w, err)
					return
				}
			}
			existing[c.Key] = saved
			if there {
				res.Updated++
			} else {
				res.Added++
			}
		}
	}
	if err := s.Phonebook.Reload(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// clip shortens s to at most n characters.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}
