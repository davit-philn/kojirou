// Package device prepares manga pages for the screen of a reading device:
// it brings every page to the device's exact resolution, without cutting
// into the picture, and can make the pages clearer on a grayscale screen.
package device

import (
	"image"
	"image/color"
	"sort"

	"golang.org/x/image/draw"
)

// Profile is a screen.
type Profile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Profiles lists the supported screens (portrait, in pixels).
var Profiles = []Profile{
	{"kindle-pw3", "Kindle Paperwhite 3 (6\", 1072×1448)", 1072, 1448},
	{"kindle-pw4", "Kindle Paperwhite 4 (6\", 1072×1448)", 1072, 1448},
	{"kindle-pw5", "Kindle Paperwhite 5 (6.8\", 1236×1648)", 1236, 1648},
	{"kindle-oasis", "Kindle Oasis (7\", 1264×1680)", 1264, 1680},
	{"kindle-basic", "Kindle cơ bản (6\", 758×1024)", 758, 1024},
	{"kobo-clara", "Kobo Clara (6\", 1072×1448)", 1072, 1448},
}

// Lookup finds a profile by its ID.
func Lookup(id string) (Profile, bool) {
	for _, p := range Profiles {
		if p.ID == id {
			return p, true
		}
	}

	return Profile{}, false
}

// Mode says what to do when a page's shape differs from the screen's.
type Mode string

const (
	// Fit keeps the whole page and pads what is left over.
	Fit Mode = "fit"
	// Smart stretches a page whose shape is almost the screen's (by at most
	// MaxStretch) so that it fills the screen, and fits any other page.
	Smart Mode = "smart"
	// Cover fills the screen and cuts off what sticks out.
	Cover Mode = "cover"
)

// DefaultMaxStretch is how far Smart may distort a page: 8% is hard to see.
const DefaultMaxStretch = 0.08

// Options says how pages are prepared. The zero value changes nothing.
type Options struct {
	Profile Profile
	Mode    Mode
	// MaxStretch is Smart's limit; 0 means DefaultMaxStretch.
	MaxStretch float64
	// Enhance turns pages gray, stretches their contrast so that the paper is
	// white and the ink black, and sharpens them a little.
	Enhance bool
	// Paper is the colour used to pad a page; nil means the colour along the
	// page's own edge. Cropping a page's margins first makes that edge the
	// ink, so callers that crop pass the colour of the uncropped page.
	Paper color.Color
}

// Active reports whether pages are changed at all.
func (o Options) Active() bool { return o.Profile.Width > 0 || o.Enhance }

// Apply prepares one page.
func Apply(img image.Image, o Options) image.Image {
	if !o.Active() {
		return img
	}
	if o.Enhance {
		img = stretchLevels(toGray(img))
	}
	if o.Profile.Width > 0 && o.Profile.Height > 0 {
		img = fitToScreen(img, o)
	}
	if o.Enhance {
		img = sharpen(toGray(img))
	}

	return img
}

func toGray(img image.Image) *image.Gray {
	if g, ok := img.(*image.Gray); ok {
		return g
	}
	b := img.Bounds()
	g := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(g, g.Bounds(), img, b.Min, draw.Src)

	return g
}

// fitToScreen scales a page to the screen's size.
func fitToScreen(img image.Image, o Options) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	W, H := o.Profile.Width, o.Profile.Height
	if w == 0 || h == 0 {
		return img
	}

	mode := o.Mode
	if mode == "" {
		mode = Smart
	}
	maxStretch := o.MaxStretch
	if maxStretch <= 0 {
		maxStretch = DefaultMaxStretch
	}

	switch mode {
	case Cover:
		scale := maxFloat(float64(W)/float64(w), float64(H)/float64(h))
		nw, nh := roundUp(float64(w)*scale), roundUp(float64(h)*scale)
		scaled := scaleTo(img, nw, nh)
		x, y := (nw-W)/2, (nh-H)/2
		canvas := newCanvas(scaled, W, H, color.White)
		draw.Draw(canvas, canvas.Bounds(), scaled, image.Pt(x, y), draw.Src)

		return canvas
	case Smart:
		pageShape, screenShape := float64(w)/float64(h), float64(W)/float64(H)
		if abs(pageShape/screenShape-1) <= maxStretch {
			return scaleTo(img, W, H)
		}
	}

	scale := minFloat(float64(W)/float64(w), float64(H)/float64(h))
	nw, nh := minInt(W, roundUp(float64(w)*scale)), minInt(H, roundUp(float64(h)*scale))
	if nw == W && nh == H {
		return scaleTo(img, W, H)
	}
	paper := o.Paper
	if paper == nil {
		paper = PaperColor(img)
	}
	if o.Enhance && luminance(paper) >= 170 {
		paper = color.White // enhancing makes the paper white
	}
	canvas := newCanvas(img, W, H, paper)
	scaled := scaleTo(img, nw, nh)
	x, y := (W-nw)/2, (H-nh)/2
	draw.Draw(canvas, image.Rect(x, y, x+nw, y+nh), scaled, scaled.Bounds().Min, draw.Src)

	return canvas
}

// scaleTo resamples a picture to the given size.
func scaleTo(img image.Image, w, h int) image.Image {
	var dst draw.Image
	if _, gray := img.(*image.Gray); gray {
		dst = image.NewGray(image.Rect(0, 0, w, h))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)

	return dst
}

// newCanvas makes a W×H picture of the same kind as img, filled with c.
func newCanvas(img image.Image, W, H int, c color.Color) draw.Image {
	var canvas draw.Image
	if _, gray := img.(*image.Gray); gray {
		canvas = image.NewGray(image.Rect(0, 0, W, H))
	} else {
		canvas = image.NewRGBA(image.Rect(0, 0, W, H))
	}
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)

	return canvas
}

func luminance(c color.Color) int { return int(color.GrayModel.Convert(c).(color.Gray).Y) }

// PaperColor is the colour of a page's margin: the median of the pixels along
// its edge, so that padding does not show as a border.
func PaperColor(img image.Image) color.Color {
	b := img.Bounds()
	step := maxInt(1, (b.Dx()+b.Dy())/400)
	var rs, gs, bs []int
	add := func(x, y int) {
		r, g, bl, _ := img.At(x, y).RGBA()
		rs, gs, bs = append(rs, int(r>>8)), append(gs, int(g>>8)), append(bs, int(bl>>8))
	}
	ring := maxInt(1, minInt(b.Dx(), b.Dy())/100)
	for x := b.Min.X; x < b.Max.X; x += step {
		for d := 0; d < ring; d++ {
			add(x, b.Min.Y+d)
			add(x, b.Max.Y-1-d)
		}
	}
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for d := 0; d < ring; d++ {
			add(b.Min.X+d, y)
			add(b.Max.X-1-d, y)
		}
	}
	if len(rs) == 0 {
		return color.White
	}

	return color.RGBA{uint8(median(rs)), uint8(median(gs)), uint8(median(bs)), 255}
}

func median(values []int) int {
	sort.Ints(values)

	return values[len(values)/2]
}

// stretchLevels makes the paper white and the ink black: it clips the darkest
// 0.5% and maps the paper tone (a high percentile, kept between 200 and 255)
// to white.
func stretchLevels(g *image.Gray) *image.Gray {
	var hist [256]int
	total := 0
	b := g.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := g.Pix[(y-b.Min.Y)*g.Stride : (y-b.Min.Y)*g.Stride+b.Dx()]
		for _, v := range row {
			hist[v]++
			total++
		}
	}
	if total == 0 {
		return g
	}
	low, high := percentile(hist, total, 0.005), percentile(hist, total, 0.97)
	low = minInt(low, 60)
	high = maxInt(high, 200)
	if high-low < 32 {
		return g
	}

	var lut [256]uint8
	for v := range lut {
		x := (v - low) * 255 / (high - low)
		lut[v] = uint8(clamp(x, 0, 255))
	}
	out := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		src := g.Pix[y*g.Stride : y*g.Stride+b.Dx()]
		dst := out.Pix[y*out.Stride : y*out.Stride+b.Dx()]
		for x, v := range src {
			dst[x] = lut[v]
		}
	}

	return out
}

func percentile(hist [256]int, total int, p float64) int {
	target := int(float64(total) * p)
	sum := 0
	for v, n := range hist {
		sum += n
		if sum > target {
			return v
		}
	}

	return 255
}

// sharpen applies a mild unsharp mask, which keeps the thin lines of manga
// crisp after scaling.
func sharpen(g *image.Gray) *image.Gray {
	b := g.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewGray(image.Rect(0, 0, w, h))
	at := func(x, y int) int {
		x, y = clamp(x, 0, w-1), clamp(y, 0, h-1)
		return int(g.Pix[y*g.Stride+x])
	}
	const amount = 0.6
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := at(x, y)
			edge := 4*c - at(x-1, y) - at(x+1, y) - at(x, y-1) - at(x, y+1)
			out.Pix[y*out.Stride+x] = uint8(clamp(c+int(amount*float64(edge)), 0, 255))
		}
	}

	return out
}

// Rotate turns a picture a quarter turn clockwise.
func Rotate(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if g, ok := img.(*image.Gray); ok {
		out := image.NewGray(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.Pix[x*out.Stride+(h-1-y)] = g.Pix[y*g.Stride+x]
			}
		}

		return out
	}
	out := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(h-1-y, x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}

	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}

	return v
}

func roundUp(f float64) int {
	n := int(f + 0.5)
	if n < 1 {
		return 1
	}

	return n
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}

	return f
}

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}

	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}

	return b
}
