package web

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/f0rmatting/haobot/internal/agent/phonebook"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

// The yellow pages ship with the agent; the user's changes to them are
// rows of yellow_orgs laid over the built-in ones (phonebook.Pages).

func (s *Server) routeYellow(authed func(string, http.HandlerFunc)) {
	authed("GET /api/yellowpages", s.yellowPages)
	authed("GET /api/yellowpages/logos/{file}", s.yellowLogo)
	authed("GET /api/yellowpages/orgs/{id}/logo", s.orgLogo)
	authed("POST /api/yellowpages/orgs", s.saveOrg)
	authed("DELETE /api/yellowpages/orgs/{id}", s.deleteOrg)
	authed("POST /api/yellowpages/hide", s.hideOrg)
	authed("POST /api/yellowpages/unhide", s.unhideOrgs)
	authed("GET /api/yellowpages/export", s.exportYellow)
	authed("POST /api/yellowpages/import", s.importYellow)
}

// orgLogoURL changes with the organisation, so browsers may cache it.
func orgLogoURL(o store.YellowOrg) string {
	return "/api/yellowpages/orgs/" + strconv.FormatInt(o.ID, 10) + "/logo?v=" + strconv.FormatInt(o.UpdatedAt.Unix(), 10)
}

type orgJSON struct {
	ID       int64                `json:"id,omitempty"`
	Builtin  string               `json:"builtin,omitempty"`
	Name     string               `json:"name"`
	Category string               `json:"category"`
	Logo     string               `json:"logo,omitempty"` // picture URL
	Numbers  []store.YellowNumber `json:"numbers"`
	// Edited says a built-in organisation differs from how it ships.
	Edited bool `json:"edited,omitempty"`
}

func toOrgJSON(o phonebook.Org) orgJSON {
	j := orgJSON{ID: o.ID, Builtin: o.Builtin, Name: o.Name, Category: o.Category, Numbers: o.Numbers, Edited: o.ID != 0 && o.Builtin != ""}
	if o.Edit.HasLogo {
		j.Logo = orgLogoURL(o.Edit)
	} else if o.Logo != "" {
		j.Logo = "/api/yellowpages/logos/" + o.Logo
	}
	return j
}

func (s *Server) yellowPages(w http.ResponseWriter, r *http.Request) {
	p := s.Phonebook.Pages()
	orgs := make([]orgJSON, 0, len(p.Orgs))
	for _, o := range p.Orgs {
		orgs = append(orgs, toOrgJSON(o))
	}
	hidden := make([]orgJSON, 0, len(p.Hidden))
	for _, o := range p.Hidden {
		hidden = append(hidden, toOrgJSON(o))
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": p.Source, "orgs": orgs, "hidden": hidden})
}

// yellowLogo serves a built-in logo; names are content hashes.
func (s *Server) yellowLogo(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	data, err := fs.ReadFile(phonebook.Logos, name)
	if err != nil || strings.Contains(name, "/") {
		writeError(w, http.StatusNotFound, "没有这个 logo")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Type", "image/png")
	w.Write(data)
}

func (s *Server) orgLogo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的机构")
		return
	}
	data, err := s.Store.YellowLogo(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "没有 logo")
		return
	} else if err != nil {
		s.serverError(w, err)
		return
	}
	// The URL carries the organisation's version (orgLogoURL).
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Write(data)
}

// orgRequest is an organisation as the UI edits it and a yellow pages file
// holds it.
type orgRequest struct {
	ID       int64                `json:"id,omitempty"`
	Builtin  string               `json:"builtin,omitempty"`
	Name     string               `json:"name"`
	Category string               `json:"category"`
	Numbers  []store.YellowNumber `json:"numbers"`
	// Logo is a data: URL of a JPEG, PNG or WebP; "" removes the user's
	// logo and leaving it out keeps it.
	Logo   *string `json:"logo,omitempty"`
	Hidden bool    `json:"hidden,omitempty"`
}

// clean trims and checks an organisation; numbers listed twice are kept once.
func (o *orgRequest) clean() error {
	o.Name, o.Category = strings.TrimSpace(o.Name), strings.TrimSpace(o.Category)
	if o.Category == "" {
		o.Category = "其它"
	}
	switch {
	case o.Name == "":
		return errors.New("请填写机构名称")
	case utf8.RuneCountInString(o.Name) > 64 || utf8.RuneCountInString(o.Category) > 32:
		return errors.New("名称或分类太长")
	case len(o.Numbers) > 50:
		return errors.New("号码太多，最多 50 个")
	}
	seen := map[string]bool{}
	numbers := []store.YellowNumber{}
	for _, n := range o.Numbers {
		n.Number, n.Label = strings.TrimSpace(n.Number), strings.TrimSpace(n.Label)
		k := phonebook.Key(n.Number)
		switch {
		case k == "":
			continue
		case len(n.Number) > 32 || utf8.RuneCountInString(n.Label) > 32:
			return errors.New("号码或号码说明太长")
		case seen[k]:
			continue
		}
		seen[k] = true
		numbers = append(numbers, n)
	}
	o.Numbers = numbers
	return nil
}

// orgLogoData reads a logo from a data: URL, shrinking a large one.
func orgLogoData(dataURL string) ([]byte, error) {
	_, b64, ok := strings.Cut(dataURL, ";base64,")
	if !ok || !strings.HasPrefix(dataURL, "data:image/") {
		return nil, errors.New("logo 格式不对")
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("logo 格式不对")
	}
	if data = fitAvatar(data); data == nil {
		return nil, errors.New("logo 只支持 JPEG、PNG 或 WebP 图片")
	}
	return data, nil
}

// builtinOrg finds a built-in organisation by the name it ships with.
func builtinOrg(name string) (phonebook.Org, bool) {
	for _, o := range phonebook.Yellow().Orgs {
		if o.Name == name {
			return o, true
		}
	}
	return phonebook.Org{}, false
}

// storeOrg saves o as the change it is, with its logo.
func (s *Server) storeOrg(r *http.Request, o orgRequest) (store.YellowOrg, error) {
	var logo []byte
	if o.Logo != nil && *o.Logo != "" {
		var err error
		if logo, err = orgLogoData(*o.Logo); err != nil {
			return store.YellowOrg{}, err
		}
	}
	saved, err := s.Store.SaveYellowOrg(r.Context(), store.YellowOrg{ID: o.ID, Builtin: o.Builtin, Name: o.Name, Category: o.Category, Numbers: o.Numbers, Hidden: o.Hidden})
	if err != nil {
		return saved, err
	}
	if o.Logo != nil {
		err = s.Store.SetYellowLogo(r.Context(), saved.ID, logo)
	}
	return saved, err
}

// saveOrg saves an edit of a built-in organisation (builtin set), or adds or
// changes one of the user's own.
func (s *Server) saveOrg(w http.ResponseWriter, r *http.Request) {
	var req orgRequest
	if !decode(w, r, &req) {
		return
	}
	if err := req.clean(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Hidden = false
	if req.ID != 0 {
		// The row decides which organisation it changes, not the request.
		i := slices.IndexFunc(s.Phonebook.Pages().Orgs, func(o phonebook.Org) bool { return o.ID == req.ID })
		if i < 0 {
			writeError(w, http.StatusNotFound, "机构不存在")
			return
		}
		req.Builtin = s.Phonebook.Pages().Orgs[i].Builtin
	} else if _, ok := builtinOrg(req.Builtin); req.Builtin != "" && !ok {
		writeError(w, http.StatusNotFound, "内置黄页里没有这个机构")
		return
	}
	saved, err := s.storeOrg(r, req)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "机构不存在")
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reloadPhonebook(w, r, map[string]any{"id": saved.ID})
}

// deleteOrg removes the user's own organisation, or puts an edited built-in
// one back as it ships.
func (s *Server) deleteOrg(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的机构")
		return
	}
	if err := s.Store.DeleteYellowOrg(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "机构不存在")
		return
	} else if err != nil {
		s.serverError(w, err)
		return
	}
	s.reloadPhonebook(w, r, nil)
}

// hideOrg removes a built-in organisation from the user's yellow pages.
func (s *Server) hideOrg(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Builtin string `json:"builtin"`
	}
	if !decode(w, r, &req) {
		return
	}
	o, ok := builtinOrg(req.Builtin)
	if !ok {
		writeError(w, http.StatusNotFound, "内置黄页里没有这个机构")
		return
	}
	// A hidden organisation keeps no edits: unhiding brings it back as it ships.
	if _, err := s.Store.SaveYellowOrg(r.Context(), store.YellowOrg{Builtin: o.Name, Name: o.Name, Category: o.Category, Hidden: true}); err != nil {
		s.serverError(w, err)
		return
	}
	s.reloadPhonebook(w, r, nil)
}

func (s *Server) unhideOrgs(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.UnhideYellowOrgs(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	s.reloadPhonebook(w, r, nil)
}

// reloadPhonebook answers a change once the phone book has it.
func (s *Server) reloadPhonebook(w http.ResponseWriter, r *http.Request, body map[string]any) {
	if err := s.Phonebook.Reload(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	if body == nil {
		ok(w)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// yellowFile is an exported yellow pages: every organisation as the user
// sees it, the hidden built-in ones too, with the logos the user gave.
type yellowFile struct {
	Format     string       `json:"format"`
	ExportedAt time.Time    `json:"exportedAt"`
	Orgs       []orgRequest `json:"orgs"`
}

const yellowFormat = "haobot-yellowpages/1"

func (s *Server) exportYellow(w http.ResponseWriter, r *http.Request) {
	p := s.Phonebook.Pages()
	file := yellowFile{Format: yellowFormat, ExportedAt: time.Now(), Orgs: []orgRequest{}}
	for _, list := range [][]phonebook.Org{p.Orgs, p.Hidden} {
		for _, o := range list {
			out := orgRequest{Builtin: o.Builtin, Name: o.Name, Category: o.Category, Numbers: o.Numbers, Hidden: o.Edit.Hidden}
			if o.Edit.HasLogo {
				data, err := s.Store.YellowLogo(r.Context(), o.ID)
				if err != nil {
					s.serverError(w, err)
					return
				}
				logo := "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
				out.Logo = &logo
			}
			file.Orgs = append(file.Orgs, out)
		}
	}
	attachment(w, "号小助黄页-"+time.Now().Format("20060102")+".json")
	writeJSON(w, http.StatusOK, file)
}

// importYellow makes the yellow pages like a file's: a built-in
// organisation takes the file's version (back to how it ships when they
// agree), the user's own are matched by name. Organisations the file does
// not list are left alone.
func (s *Server) importYellow(w http.ResponseWriter, r *http.Request) {
	var file yellowFile
	if !decodeLimit(w, r, &file, maxImport) {
		return
	}
	if file.Format != yellowFormat || len(file.Orgs) == 0 {
		writeError(w, http.StatusBadRequest, "这不是号小助导出的黄页文件")
		return
	}
	edits, err := s.Store.YellowOrgs(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	byBuiltin, own := map[string]int64{}, map[string]int64{}
	for _, e := range edits {
		if e.Builtin != "" {
			byBuiltin[e.Builtin] = e.ID
		} else {
			own[strings.ToUpper(e.Name)] = e.ID
		}
	}
	var res importResult
	for _, o := range file.Orgs {
		if err := o.clean(); err != nil {
			res.Skipped++
			continue
		}
		o.ID = 0
		if o.Builtin == "" {
			// A hand-written file may name a built-in organisation.
			if _, ok := builtinOrg(o.Name); ok {
				o.Builtin = o.Name
			}
		}
		b, isBuiltin := builtinOrg(o.Builtin)
		switch {
		case isBuiltin && !o.Hidden && o.Logo == nil && o.Name == b.Name && o.Category == b.Category && slices.Equal(o.Numbers, b.Numbers):
			id, changed := byBuiltin[o.Builtin]
			if !changed {
				res.Skipped++
				continue
			}
			if err := s.Store.DeleteYellowOrg(r.Context(), id); err != nil {
				s.serverError(w, err)
				return
			}
			delete(byBuiltin, o.Builtin)
			res.Updated++
			continue
		case isBuiltin:
			o.ID = byBuiltin[o.Builtin]
		default:
			o.Builtin, o.Hidden = "", false
			o.ID = own[strings.ToUpper(o.Name)]
		}
		// The file's logo replaces the user's, and no logo removes it; one
		// that cannot be read is dropped rather than failing the import.
		if o.Logo == nil {
			o.Logo = new(string)
		} else if _, err := orgLogoData(*o.Logo); err != nil {
			o.Logo = new(string)
		}
		saved, err := s.storeOrg(r, o)
		if err != nil {
			s.serverError(w, err)
			return
		}
		if o.ID != 0 {
			res.Updated++
		} else {
			res.Added++
		}
		if saved.Builtin != "" {
			byBuiltin[saved.Builtin] = saved.ID
		} else {
			own[strings.ToUpper(saved.Name)] = saved.ID
		}
	}
	if err := s.Phonebook.Reload(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
