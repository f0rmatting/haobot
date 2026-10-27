package web

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // for image.Decode
	"net/http"
)

// avatarPx is the side of the square JPEG an imported photo becomes, as
// the UI makes of a picked one (ContactModal.tsx).
const avatarPx = 192

// fitAvatar makes an imported picture fit maxAvatar: one that already does
// is kept, a larger JPEG or PNG is cropped to its centre square and shrunk.
// It returns nil for a picture it cannot use.
func fitAvatar(data []byte) []byte {
	if len(data) <= maxAvatar && avatarTypes[http.DetectContentType(data)] {
		return data
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	x0, y0 := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	size := min(side, avatarPx)
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	// Average the source pixels under each destination pixel; JPEG has no
	// transparency, so transparent pixels count as white.
	for y := 0; y < size; y++ {
		sy0, sy1 := y0+y*side/size, y0+(y+1)*side/size
		for x := 0; x < size; x++ {
			sx0, sx1 := x0+x*side/size, x0+(x+1)*side/size
			var r, g, bl, n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					white := uint64(0xffff - ca)
					r, g, bl, n = r+uint64(cr)+white, g+uint64(cg)+white, bl+uint64(cb)+white, n+1
				}
			}
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), 0xffff})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil || out.Len() > maxAvatar {
		return nil
	}
	return out.Bytes()
}
