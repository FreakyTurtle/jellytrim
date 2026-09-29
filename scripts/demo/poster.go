package main

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- picks colours, not security
	"image"
	"image/color"
	"image/png"
)

// Posters are flat geometric artwork, one per title, so the Library looks
// like a real one without using anyone's artwork. The fake Jellyfin keeps
// each item's poster, so each is drawn once.

const posterW, posterH = 240, 360

// posterInks are muted colours that sit well on the UI's warm off-white.
var posterInks = []color.RGBA{
	{0x14, 0x14, 0x14, 0xff}, // near-black
	{0xFF, 0x4F, 0x00, 0xff}, // signal orange
	{0x24, 0x50, 0xB2, 0xff}, // blue
	{0x1E, 0x7A, 0x3C, 0xff}, // green
	{0x8A, 0x5C, 0x00, 0xff}, // ochre
	{0xB4, 0x23, 0x18, 0xff}, // red
	{0x3B, 0x5B, 0x7A, 0xff}, // slate
	{0x5A, 0x3A, 0x5A, 0xff}, // plum
	{0x2F, 0x6F, 0x6A, 0xff}, // teal
}

var posterPapers = []color.RGBA{
	{0xF4, 0xF1, 0xEA, 0xff},
	{0xEC, 0xE8, 0xDF, 0xff},
	{0xD9, 0xC9, 0xA8, 0xff},
	{0xCF, 0xC9, 0xBC, 0xff},
}

// posters draws each title's poster once; a series shares one across its
// seasons and episodes.
type posters map[string][]byte

func (p posters) get(title string) []byte {
	if b, ok := p[title]; ok {
		return b
	}
	p[title] = poster(title)
	return p[title]
}

// poster returns a PNG for title, the same every time.
func poster(title string) []byte {
	sum := md5.Sum([]byte(title)) // #nosec G401 -- picks colours, not security
	img := image.NewRGBA(image.Rect(0, 0, posterW, posterH))
	paper := posterPapers[int(sum[0])%len(posterPapers)]
	ink := posterInks[int(sum[1])%len(posterInks)]
	second := posterInks[(int(sum[1])+1+int(sum[2])%(len(posterInks)-1))%len(posterInks)]
	fill(img, img.Bounds(), paper)
	switch sum[3] % 4 {
	case 0: // a sun over a horizon
		fill(img, image.Rect(0, posterH*3/5, posterW, posterH), ink)
		disc(img, posterW/2, posterH*3/5-10, 40+int(sum[4])%40, second)
	case 1: // bands
		n := 3 + int(sum[4])%4
		for i := 0; i < n; i += 2 {
			fill(img, image.Rect(0, posterH*i/n, posterW, posterH*(i+1)/n), ink)
		}
		fill(img, image.Rect(posterW/3, 0, posterW/3+14, posterH), second)
	case 2: // a big disc cut by a bar
		disc(img, int(sum[4])%posterW, posterH/3+int(sum[5])%(posterH/3), 90, ink)
		fill(img, image.Rect(0, posterH-70, posterW, posterH-54), second)
	default: // blocks
		for i := 0; i < 6; i++ {
			x, y := int(sum[4+i])%(posterW-60), int(sum[10+i%6])%(posterH-60)
			c := ink
			if i%3 == 0 {
				c = second
			}
			fill(img, image.Rect(x, y, x+60, y+60), c)
		}
	}
	fill(img, image.Rect(0, 0, posterW, 2), posterInks[0])
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return
	}
	row := make([]byte, 4*r.Dx())
	for i := 0; i < len(row); i += 4 {
		row[i], row[i+1], row[i+2], row[i+3] = c.R, c.G, c.B, c.A
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(img.Pix[img.PixOffset(r.Min.X, y):], row)
	}
}

func disc(img *image.RGBA, cx, cy, radius int, c color.RGBA) {
	for dy := -radius; dy <= radius; dy++ {
		dx := 0
		for (dx+1)*(dx+1)+dy*dy <= radius*radius {
			dx++
		}
		fill(img, image.Rect(cx-dx, cy+dy, cx+dx+1, cy+dy+1), c)
	}
}
