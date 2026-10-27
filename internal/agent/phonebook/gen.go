//go:build ignore

// gen builds yellowpages.json and logos/ from the data directory of
// https://github.com/metowolf/vCards (MIT): one YAML file per organisation,
// in a directory per category, with its logo as a PNG beside it. Run it
// with `make yellowpages`.
//
// Logos are shrunk to logoSize pixels and named by a hash of their
// content, so the agent can serve them as immutable.
//
// The files follow a fixed schema (src/const/schema.ts upstream), so a line
// reader is enough and the agent needs no YAML dependency:
//
//	basic:
//	  organization: 比亚迪
//	  cellPhone:
//	    - 95188
//	    - number: 4001-666-016
//	      label: 客户回访
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// logoSize is the logos' largest side: twice the 48px they are shown at.
const logoSize = 96

type number struct {
	Number string `json:"number"`
	Label  string `json:"label,omitempty"`
}

type org struct {
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Numbers  []number `json:"numbers"`
	Logo     string   `json:"logo,omitempty"` // file in logos/
}

func main() {
	src := flag.String("src", "", "vCards checkout")
	rev := flag.String("rev", "", "upstream commit, recorded in the output")
	out := flag.String("out", "yellowpages.json", "output file")
	logos := flag.String("logos", "logos", "logo directory, replaced")
	flag.Parse()
	if *src == "" {
		fmt.Fprintln(os.Stderr, "usage: go run gen.go -src <vCards checkout> [-rev <commit>] [-out yellowpages.json]")
		os.Exit(2)
	}
	files, err := filepath.Glob(filepath.Join(*src, "data", "*", "*.yaml"))
	if err != nil || len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no data/*/*.yaml under", *src)
		os.Exit(1)
	}
	if err := os.RemoveAll(*logos); err != nil {
		fail(err)
	}
	if err := os.MkdirAll(*logos, 0o755); err != nil {
		fail(err)
	}
	var orgs []org
	logoBytes := 0
	for _, f := range files {
		o, err := parse(f)
		if err != nil {
			fail(fmt.Errorf("%s: %w", f, err))
		}
		// Organisations without numbers are kept: SMS signed 【Steam】 come
		// from gateway numbers, and the signature still finds their logo.
		if o.Numbers == nil {
			o.Numbers = []number{}
		}
		pic := strings.TrimSuffix(f, ".yaml") + ".png"
		if _, err := os.Stat(pic); err == nil {
			data, err := logo(pic)
			if err != nil {
				fail(fmt.Errorf("%s: %w", pic, err))
			}
			sum := sha256.Sum256(data)
			o.Logo = hex.EncodeToString(sum[:8]) + ".png"
			if err := os.WriteFile(filepath.Join(*logos, o.Logo), data, 0o644); err != nil {
				fail(err)
			}
			logoBytes += len(data)
		}
		orgs = append(orgs, o)
	}
	sort.Slice(orgs, func(i, j int) bool {
		if orgs[i].Category != orgs[j].Category {
			return orgs[i].Category < orgs[j].Category
		}
		return orgs[i].Name < orgs[j].Name
	})
	source := "https://github.com/metowolf/vCards"
	if *rev != "" {
		source += "@" + *rev
	}
	data, err := json.MarshalIndent(struct {
		Source string `json:"source"`
		Orgs   []org  `json:"orgs"`
	}{source, orgs}, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		fail(err)
	}
	n, withLogo := 0, 0
	for _, o := range orgs {
		n += len(o.Numbers)
		if o.Logo != "" {
			withLogo++
		}
	}
	fmt.Printf("%d organisations, %d numbers -> %s; %d logos, %d KB -> %s/\n", len(orgs), n, *out, withLogo, logoBytes>>10, *logos)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// logo reads a PNG and returns it shrunk to logoSize.
func logo(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, shrink(img, logoSize)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// shrink scales img down so its larger side is size, averaging the source
// pixels under each destination pixel (the standard library has no
// resampler, and the agent does not need one).
func shrink(img image.Image, size int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= size && h <= size {
		return img
	}
	dw, dh := size, h*size/w
	if h > w {
		dw, dh = w*size/h, size
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := b.Min.Y+y*h/dh, b.Min.Y+(y+1)*h/dh
		for x := 0; x < dw; x++ {
			x0, x1 := b.Min.X+x*w/dw, b.Min.X+(x+1)*w/dw
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					r, g, bl, a, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca), n+1
				}
			}
			// RGBA() is alpha-premultiplied; NRGBA.Set converts back.
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return dst
}

func parse(path string) (org, error) {
	f, err := os.Open(path)
	if err != nil {
		return org{}, err
	}
	defer f.Close()
	o := org{Category: filepath.Base(filepath.Dir(path))}
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 2:
			key, value, _ := strings.Cut(text, ":")
			section = key
			if key == "organization" {
				o.Name = unquote(value)
			}
		case section != "cellPhone":
		case strings.HasPrefix(text, "- "):
			item := strings.TrimPrefix(text, "- ")
			if v, ok := strings.CutPrefix(item, "number:"); ok {
				item = v
			}
			o.Numbers = append(o.Numbers, number{Number: unquote(item)})
		case strings.HasPrefix(text, "label:") && len(o.Numbers) > 0:
			o.Numbers[len(o.Numbers)-1].Label = unquote(strings.TrimPrefix(text, "label:"))
		}
	}
	if o.Name == "" {
		return o, fmt.Errorf("no organization")
	}
	return o, sc.Err()
}

func unquote(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"'`)
}
