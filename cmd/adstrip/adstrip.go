// Package adstrip finds and removes advertising strips that a scanlation site
// adds to the top or the bottom of the first and last page of each chapter.
//
// Such a strip is the same picture on page after page, so it is found by
// comparing pages: the rows at the top (or bottom) that look alike on several
// pages are the strip. Pictures are compared at a fixed width, so a strip that
// was scaled differently on two pages still matches.
package adstrip

import (
	"encoding/base64"
	"encoding/json"
	"image"
	"image/draw"
	"math"
)

const (
	// Width is the width pages are compared at.
	Width = 96
	// minRows is the smallest strip, about a tenth of a page's width.
	minRows = 10
	// seedRows are the rows used to decide that two pages may share a strip.
	seedRows = 4
	// rowTolerance is how far two rows may differ (mean difference of
	// brightness, 0 to 255): re-encoding a picture changes it a little.
	rowTolerance = 12.0
	// stripTolerance is the same for the mean over all rows of a strip.
	stripTolerance = 9.0
	// contentSpread is the brightness variation along a row that counts as
	// picture rather than blank paper.
	contentSpread = 12.0
)

// Position says whether a strip is at the top or the bottom of a page.
type Position string

const (
	Top    Position = "top"
	Bottom Position = "bottom"
	// Page is a whole picture that is an advertisement: it is dropped.
	PageAd Position = "page"
)

// Strip is one advertising strip: what it looks like at the comparison width.
type Strip struct {
	Position Position `json:"position"`
	Rows     int      `json:"rows"`
	// Sig is the strip's brightness, Rows × Width values (row by row; the
	// row nearest the page's edge first).
	Sig []byte `json:"sig"`
}

// MarshalJSON stores the signature compactly.
func (s Strip) MarshalJSON() ([]byte, error) {
	type plain struct {
		Position Position `json:"position"`
		Rows     int      `json:"rows"`
		Sig      string   `json:"sig"`
	}

	return json.Marshal(plain{s.Position, s.Rows, base64.StdEncoding.EncodeToString(s.Sig)})
}

// UnmarshalJSON reads what MarshalJSON wrote.
func (s *Strip) UnmarshalJSON(data []byte) error {
	var plain struct {
		Position Position `json:"position"`
		Rows     int      `json:"rows"`
		Sig      string   `json:"sig"`
	}
	if err := json.Unmarshal(data, &plain); err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(plain.Sig)
	if err != nil {
		return err
	}
	if plain.Rows <= 0 || len(sig) != plain.Rows*Width || (plain.Position != Top && plain.Position != Bottom && plain.Position != PageAd) {
		return errInvalidStrip
	}
	s.Position, s.Rows, s.Sig = plain.Position, plain.Rows, sig

	return nil
}

type stripError string

func (e stripError) Error() string { return string(e) }

const errInvalidStrip = stripError("invalid advertising strip")

// Page is a page reduced to its brightness at the comparison width.
type Page struct {
	// Rows is the number of rows at the comparison width.
	Rows int
	// Pix is Rows × Width brightness values, top row first.
	Pix []byte
	// SourceWidth and SourceHeight are the picture's real size.
	SourceWidth, SourceHeight int
	// Group says which chapter or file the page comes from. A whole page is an
	// advertisement only if it turns up in at least two groups: a picture
	// repeated inside one file (a cover that was added) is not one.
	Group int
}

// Analyze reduces a picture so that it can be compared with others.
func Analyze(img image.Image) *Page {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return &Page{}
	}
	rows := int(math.Round(float64(h) * Width / float64(w)))
	if rows < 1 {
		rows = 1
	}
	sum := make([]uint32, rows*Width)
	count := make([]uint32, rows*Width)
	luma := lumaReader(img)
	for y := 0; y < h; y++ {
		cy := y * rows / h
		for x := 0; x < w; x++ {
			cx := x * Width / w
			i := cy*Width + cx
			sum[i] += uint32(luma(b.Min.X+x, b.Min.Y+y))
			count[i]++
		}
	}
	pix := make([]byte, rows*Width)
	for i := range pix {
		if count[i] > 0 {
			pix[i] = byte(sum[i] / count[i])
		}
	}

	return &Page{Rows: rows, Pix: pix, SourceWidth: w, SourceHeight: h}
}

// lumaReader returns a fast brightness lookup for the picture types decoders
// produce.
func lumaReader(img image.Image) func(x, y int) uint8 {
	switch m := img.(type) {
	case *image.Gray:
		return func(x, y int) uint8 { return m.Pix[(y-m.Rect.Min.Y)*m.Stride+(x-m.Rect.Min.X)] }
	case *image.YCbCr:
		return func(x, y int) uint8 { return m.Y[m.YOffset(x, y)] }
	default:
		return func(x, y int) uint8 {
			r, g, bl, _ := img.At(x, y).RGBA()
			return uint8((299*r + 587*g + 114*bl) / 1000 >> 8)
		}
	}
}

func (p *Page) row(i int) []byte { return p.Pix[i*Width : (i+1)*Width] }

// flipped returns the page upside down, so that bottom strips can be found
// with the code for top strips.
func (p *Page) flipped() *Page {
	out := &Page{Rows: p.Rows, Pix: make([]byte, len(p.Pix)), SourceWidth: p.SourceWidth, SourceHeight: p.SourceHeight}
	for i := 0; i < p.Rows; i++ {
		copy(out.Pix[(p.Rows-1-i)*Width:], p.row(i))
	}

	return out
}

func rowDistance(a, b []byte) float64 {
	var sum int
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += d
	}

	return float64(sum) / float64(len(a))
}

func rowSpread(r []byte) float64 {
	var sum, sq float64
	for _, v := range r {
		sum += float64(v)
		sq += float64(v) * float64(v)
	}
	n := float64(len(r))
	variance := sq/n - (sum/n)*(sum/n)
	if variance < 0 {
		return 0
	}

	return math.Sqrt(variance)
}

// commonTop is how many rows from the top two pages have in common.
func commonTop(a, b *Page) int {
	n := a.Rows
	if b.Rows < n {
		n = b.Rows
	}
	for i := 0; i < n; i++ {
		if rowDistance(a.row(i), b.row(i)) > rowTolerance {
			return i
		}
	}

	return n
}

// hasContent reports whether rows look like a picture (not blank paper).
func hasContent(rows []byte, n int) bool {
	content := 0
	for i := 0; i < n; i++ {
		if rowSpread(rows[i*Width:(i+1)*Width]) >= contentSpread {
			content++
		}
	}

	return n > 0 && float64(content) >= 0.3*float64(n)
}

// detectTop finds strips that several pages share at their top.
func detectTop(pages []*Page) []Strip {
	type group struct {
		ref     *Page
		members []*Page
	}
	var groups []*group
	for _, p := range pages {
		if p.Rows < minRows {
			continue
		}
		placed := false
		for _, g := range groups {
			if commonTop(g.ref, p) >= seedRows {
				g.members = append(g.members, p)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, &group{ref: p, members: []*Page{p}})
		}
	}

	var strips []Strip
	for _, g := range groups {
		if len(g.members) < 2 {
			continue
		}
		length := g.ref.Rows
		for _, m := range g.members[1:] {
			if c := commonTop(g.ref, m); c < length {
				length = c
			}
		}
		// Blank rows at the end are paper, not strip.
		for length > 0 && rowSpread(g.ref.row(length-1)) < contentSpread/2 {
			length--
		}
		if length < minRows || !hasContent(g.ref.Pix, length) {
			continue
		}
		strips = append(strips, Strip{Position: Top, Rows: length, Sig: append([]byte(nil), g.ref.Pix[:length*Width]...)})
	}

	return strips
}

// Detect finds the strips that appear on the top or the bottom of at least
// two of the given pages.
func Detect(pages []*Page) []Strip {
	flipped := make([]*Page, len(pages))
	for i, p := range pages {
		flipped[i] = p.flipped()
	}
	strips := detectTop(pages)
	for _, s := range detectTop(flipped) {
		strips = append(strips, Strip{Position: Bottom, Rows: s.Rows, Sig: s.Sig})
	}
	// A picture that is the same on several pages is an advertisement of its
	// own, so a strip it would give is moot; keep the whole-page ones apart.
	whole := detectPages(pages)
	// Two copies of an advertising page share their whole picture, which also
	// looks like a strip at their top and bottom; those are not wanted.
	var kept []Strip
	for _, st := range strips {
		if !isPartOfAPage(st, whole) {
			kept = append(kept, st)
		}
	}

	return Merge(nil, append(kept, whole...))
}

// isPartOfAPage reports whether a strip is just the top or bottom of one of the
// advertising pages.
func isPartOfAPage(s Strip, pages []Strip) bool {
	for _, page := range pages {
		if s.Rows > page.Rows {
			continue
		}
		var total float64
		for i := 0; i < s.Rows; i++ {
			from := i
			if s.Position == Bottom {
				from = page.Rows - 1 - i // a bottom strip's rows start at the page's edge
			}
			total += rowDistance(s.Sig[i*Width:(i+1)*Width], page.Sig[from*Width:(from+1)*Width])
		}
		if total/float64(s.Rows) <= stripTolerance {
			return true
		}
	}

	return false
}

// detectPages finds whole pictures that occur on at least two of the pages.
func detectPages(pages []*Page) []Strip {
	type group struct {
		ref    *Page
		groups map[int]bool
	}
	var groups []*group
	for _, p := range pages {
		if p.Rows < minRows {
			continue
		}
		placed := false
		for _, g := range groups {
			if wholeMatch(g.ref, p) {
				g.groups[p.Group] = true
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, &group{ref: p, groups: map[int]bool{p.Group: true}})
		}
	}
	var out []Strip
	for _, g := range groups {
		if len(g.groups) >= 2 && hasContent(g.ref.Pix, g.ref.Rows) {
			out = append(out, Strip{Position: PageAd, Rows: g.ref.Rows, Sig: append([]byte(nil), g.ref.Pix...)})
		}
	}

	return out
}

// WholePage makes a strip that stands for a whole page, for a page the user
// pointed out as an advertisement.
func WholePage(p *Page) Strip {
	return Strip{Position: PageAd, Rows: p.Rows, Sig: append([]byte(nil), p.Pix...)}
}

// wholeMatch reports whether two pages are the same picture (at any size
// that has the same shape).
func wholeMatch(a, b *Page) bool {
	if absInt(a.Rows-b.Rows) > 2 {
		return false
	}
	n := a.Rows
	if b.Rows < n {
		n = b.Rows
	}
	var total float64
	for i := 0; i < n; i++ {
		d := rowDistance(a.row(i), b.row(i))
		if d > 3*rowTolerance {
			return false
		}
		total += d
		if i >= 8 && total/float64(i+1) > stripTolerance*1.5 {
			return false
		}
	}

	return total/float64(n) <= stripTolerance
}

// Merge adds strips to a list, leaving out those that look like one already
// there.
func Merge(known, more []Strip) []Strip {
	out := append([]Strip(nil), known...)
	for _, s := range more {
		duplicate := false
		for _, k := range out {
			if k.Position == s.Position && similar(k, s) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, s)
		}
	}

	return out
}

func similar(a, b Strip) bool {
	n := a.Rows
	if b.Rows < n {
		n = b.Rows
	}
	if n == 0 || absInt(a.Rows-b.Rows) > 4 {
		return false
	}
	var total float64
	for i := 0; i < n; i++ {
		total += rowDistance(a.Sig[i*Width:(i+1)*Width], b.Sig[i*Width:(i+1)*Width])
	}

	return total/float64(n) <= stripTolerance
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}

	return v
}

// matches reports whether the top (or bottom) rows of a page are the strip.
func (s Strip) matches(p *Page) bool {
	if s.Position == PageAd {
		return wholeMatch(&Page{Rows: s.Rows, Pix: s.Sig}, p)
	}
	if p.Rows <= s.Rows+2 {
		return false
	}
	var total float64
	for i := 0; i < s.Rows; i++ {
		var row []byte
		if s.Position == Top {
			row = p.row(i)
		} else {
			row = p.row(p.Rows - 1 - i)
		}
		d := rowDistance(row, s.Sig[i*Width:(i+1)*Width])
		if d > 3*rowTolerance {
			return false
		}
		total += d
	}

	return total/float64(s.Rows) <= stripTolerance
}

// Cut says how many pixels to remove from the top and the bottom of a picture.
type Cut struct {
	Top, Bottom int
	// Drop means the whole picture is an advertisement.
	Drop bool
}

// Empty reports whether nothing is cut.
func (c Cut) Empty() bool { return c.Top == 0 && c.Bottom == 0 && !c.Drop }

// Find reports what the strips cut off a page, in the page's own pixels.
func Find(p *Page, strips []Strip) Cut {
	var cut Cut
	for _, s := range strips {
		if s.Position == PageAd && s.matches(p) {
			return Cut{Drop: true}
		}
	}
	for _, s := range strips {
		if s.Position == PageAd || !s.matches(p) {
			continue
		}
		// The row where the strip ends is part picture, part strip, so it did
		// not match: the strip reaches into it, and it is cut as well. Leaving
		// it would keep a thin line of the banner on the page.
		px := int(math.Ceil(float64(s.Rows+1) * float64(p.SourceWidth) / Width))
		if s.Position == Top && px > cut.Top {
			cut.Top = px
		}
		if s.Position == Bottom && px > cut.Bottom {
			cut.Bottom = px
		}
	}
	if cut.Top+cut.Bottom >= p.SourceHeight {
		return Cut{}
	}

	return cut
}

// Crop removes the cut from a picture.
func Crop(img image.Image, cut Cut) image.Image {
	if cut.Empty() || cut.Drop {
		return img
	}
	b := img.Bounds()
	r := image.Rect(b.Min.X, b.Min.Y+cut.Top, b.Max.X, b.Max.Y-cut.Bottom)
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)

	return out
}
