package adstrip

import (
	"encoding/json"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// blockAt gives a pseudo-random brightness for a block of a pattern, so that
// the same pattern looks the same at any width.
func blockAt(seed int64, bx, by int) uint8 {
	// A cheap integer hash (splitmix64); making a rand.Rand per pixel took minutes.
	z := uint64(seed)*0x9E3779B97F4A7C15 + uint64(bx)*0xBF58476D1CE4E5B9 + uint64(by)*0x94D049BB133111EB
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z ^= z >> 31

	return uint8(30 + z%200)
}

// page makes a w-wide page: an optional banner (pattern `top`) on its top, an
// optional one (pattern `bottom`) at its bottom, and content of the given seed
// in between. The banner is 0.3 of the width high; every pixel gets a little noise.
func page(w, contentHeight int, top, bottom, content int64) *image.Gray {
	bannerH := int(0.3 * float64(w))
	h := contentHeight
	if top != 0 {
		h += bannerH
	}
	if bottom != 0 {
		h += bannerH
	}
	g := image.NewGray(image.Rect(0, 0, w, h))
	noise := rand.New(rand.NewSource(int64(w)*31 + content))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var v uint8
			switch {
			case top != 0 && y < bannerH:
				v = blockAt(top, x*16/w, y*16/w)
			case bottom != 0 && y >= h-bannerH:
				v = blockAt(bottom, x*16/w, (y-(h-bannerH))*16/w)
			default:
				yy := y
				if top != 0 {
					yy -= bannerH
				}
				v = blockAt(content, x*12/w, yy*12/w)
			}
			n := noise.Intn(7) - 3
			if int(v)+n < 0 {
				v = 0
			} else if int(v)+n > 255 {
				v = 255
			} else {
				v = uint8(int(v) + n)
			}
			g.SetGray(x, y, color.Gray{v})
		}
	}

	return g
}

// blankTop is a page with 12% white paper at its top, which several pages share.
func blankTop(w, h int, content int64) *image.Gray {
	g := page(w, h, 0, 0, content)
	for y := 0; y < h/8; y++ {
		for x := 0; x < w; x++ {
			g.SetGray(x, y, color.Gray{255})
		}
	}

	return g
}

func analyze(imgs ...image.Image) []*Page {
	out := make([]*Page, len(imgs))
	for i, img := range imgs {
		out[i] = Analyze(img)
	}

	return out
}

func TestDetectFindsABannerSharedByTheFirstPagesAndAnotherByTheLastPages(t *testing.T) {
	const pinkBanner, blueBanner = 11, 22
	pages := []image.Image{
		page(800, 900, pinkBanner, 0, 1), // first page of chapter 1
		page(800, 900, 0, 0, 2),
		page(800, 900, 0, blueBanner, 3), // last page of chapter 1
		page(700, 800, pinkBanner, 0, 4), // chapter 2 at another width
		page(700, 800, 0, 0, 5),
		page(700, 800, 0, blueBanner, 6),
		blankTop(800, 900, 7), // pages that share white paper at the top are not ads
		blankTop(800, 900, 8),
	}
	strips := Detect(analyze(pages...))

	var top, bottom *Strip
	for i := range strips {
		switch strips[i].Position {
		case Top:
			top = &strips[i]
		case Bottom:
			bottom = &strips[i]
		}
	}
	if len(strips) != 2 || top == nil || bottom == nil {
		t.Fatalf("got %d strips, want one at the top and one at the bottom: %+v", len(strips), strips)
	}
	// A banner 0.3 of the width high is about 29 rows of 96.
	for _, s := range []*Strip{top, bottom} {
		if s.Rows < 26 || s.Rows > 31 {
			t.Errorf("%v strip is %d rows, want about 29", s.Position, s.Rows)
		}
	}

	// The strips cut the banners off the pages that have them, whatever their width, and nothing else.
	wantCut := map[int]Cut{0: {Top: 240}, 2: {Bottom: 240}, 3: {Top: 210}, 5: {Bottom: 210}}
	for i, img := range pages {
		cut := Find(Analyze(img), strips)
		want := wantCut[i]
		if abs(cut.Top-want.Top) > 14 || abs(cut.Bottom-want.Bottom) > 14 {
			t.Errorf("page %d: cut %+v, want about %+v", i, cut, want)
		}
	}

	cropped := Crop(pages[0], Find(Analyze(pages[0]), strips))
	if b := cropped.Bounds(); b.Dx() != 800 || b.Dy() < 880 || b.Dy() > 920 {
		t.Errorf("cropped page is %v, want about 800x900 (the banner removed)", b)
	}
}

func TestNothingIsFoundWithoutARepeatedStrip(t *testing.T) {
	strips := Detect(analyze(page(800, 900, 0, 0, 1), page(800, 900, 0, 0, 2), page(800, 900, 0, 0, 3)))
	if len(strips) != 0 {
		t.Errorf("found %d strips in pages with nothing in common", len(strips))
	}
	// One page alone proves nothing.
	if strips := Detect(analyze(page(800, 900, 5, 0, 1))); len(strips) != 0 {
		t.Errorf("found a strip on a single page: %+v", strips)
	}
}

func TestKnownStripsAreRemovedFromASinglePage(t *testing.T) {
	known := Detect(analyze(page(800, 900, 5, 0, 1), page(800, 900, 5, 0, 2)))
	if len(known) != 1 {
		t.Fatalf("learned %d strips", len(known))
	}
	// A page of another chapter that shares nothing but the banner.
	if cut := Find(Analyze(page(760, 1100, 5, 0, 99)), known); cut.Top < 200 {
		t.Errorf("the banner was not recognised: %+v", cut)
	}
	// A page with another banner is left alone.
	if cut := Find(Analyze(page(800, 900, 6, 0, 3)), known); !cut.Empty() {
		t.Errorf("a different picture at the top was cut: %+v", cut)
	}
}

func TestStripsSurviveJSONAndMergeWithoutDuplicates(t *testing.T) {
	found := Detect(analyze(page(800, 900, 5, 0, 1), page(800, 900, 5, 0, 2)))
	data, err := json.Marshal(found)
	if err != nil {
		t.Fatal(err)
	}
	var back []Strip
	if err := json.Unmarshal(data, &back); err != nil || len(back) != 1 || back[0].Rows != found[0].Rows || string(back[0].Sig) != string(found[0].Sig) {
		t.Fatalf("round trip failed: %v %+v", err, back)
	}
	again := Detect(analyze(page(800, 900, 5, 0, 3), page(800, 900, 5, 0, 4)))
	if merged := Merge(found, again); len(merged) != 1 {
		t.Errorf("the same banner learned twice must be kept once, got %d", len(merged))
	}
	if err := json.Unmarshal([]byte(`{"position":"side","rows":3,"sig":"AAAA"}`), &Strip{}); err == nil {
		t.Error("an invalid strip must be rejected")
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}

	return v
}

// adPage is a whole picture (1.2 times as high as wide) of the given pattern.
func adPage(w int, pattern int64) *image.Gray {
	h := w * 6 / 5
	g := image.NewGray(image.Rect(0, 0, w, h))
	noise := rand.New(rand.NewSource(int64(w) + pattern))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := int(blockAt(pattern, x*16/w, y*16/w)) + noise.Intn(7) - 3
			if v < 0 {
				v = 0
			} else if v > 255 {
				v = 255
			}
			g.SetGray(x, y, color.Gray{uint8(v)})
		}
	}

	return g
}

func TestWholeAdvertisingPagesAreFoundAndDropped(t *testing.T) {
	pages := []image.Image{
		adPage(800, 71), page(800, 1000, 0, 0, 1), page(800, 1000, 0, 0, 2), adPage(800, 72), // chapter 1
		adPage(700, 71), page(700, 900, 0, 0, 3), page(700, 900, 0, 0, 4), adPage(700, 72), // chapter 2
	}
	strips := Detect(analyze(pages...))
	whole := 0
	for _, s := range strips {
		if s.Position == PageAd {
			whole++
		} else {
			t.Errorf("unexpected %v strip: it is only the top or bottom of an advertising page", s.Position)
		}
	}
	if whole != 2 {
		t.Fatalf("found %d advertising pages, want 2: %+v", whole, strips)
	}
	for i, img := range pages {
		cut := Find(Analyze(img), strips)
		isAd := i%4 == 0 || i%4 == 3
		if cut.Drop != isAd || (!isAd && !cut.Empty()) {
			t.Errorf("page %d: %+v, want drop=%v", i, cut, isAd)
		}
	}
	// An advertising page seen once before is dropped from a lone chapter too.
	if cut := Find(Analyze(adPage(760, 71)), strips); !cut.Drop {
		t.Errorf("the page was not recognised at another size: %+v", cut)
	}
}

func TestPagesThatMerelyRepeatBlankPaperAreNotAdvertisements(t *testing.T) {
	blank := func(w int) *image.Gray {
		g := image.NewGray(image.Rect(0, 0, w, w*6/5))
		for i := range g.Pix {
			g.Pix[i] = 255
		}
		return g
	}
	if strips := Detect(analyze(blank(800), blank(800), blank(700))); len(strips) != 0 {
		t.Errorf("blank pages gave %d strips", len(strips))
	}
}
