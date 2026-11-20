package device

import (
	"image"
	"image/color"
	"testing"
)

// framed makes a w×h page of the given paper tone with a black frame of the
// given thickness, 10 pixels inside its edges (a margin of paper around it), so
// that cut-off content shows up as a missing frame.
func framed(w, h int, paper uint8, thickness int) *image.Gray {
	const margin = 10
	g := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := paper
			inner := x >= margin && y >= margin && x < w-margin && y < h-margin
			onFrame := inner && (x < margin+thickness || y < margin+thickness || x >= w-margin-thickness || y >= h-margin-thickness)
			if onFrame {
				v = 0
			}
			g.SetGray(x, y, color.Gray{v})
		}
	}

	return g
}

func gray(img image.Image, x, y int) uint8 {
	return color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y
}

var pw3 = Profile{ID: "kindle-pw3", Width: 1072, Height: 1448}

func TestLookup(t *testing.T) {
	if p, ok := Lookup("kindle-pw3"); !ok || p.Width != 1072 || p.Height != 1448 {
		t.Errorf("kindle-pw3 = %+v, %v", p, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("unknown profile found")
	}
}

func TestFitKeepsTheWholePageAndPadsWithPaper(t *testing.T) {
	page := framed(600, 1000, 255, 6) // narrower than the screen
	out := Apply(page, Options{Profile: pw3, Mode: Fit})
	if b := out.Bounds(); b.Dx() != 1072 || b.Dy() != 1448 {
		t.Fatalf("size = %v, want 1072x1448", b)
	}
	// Scaled by 1.448: the page is 869 wide, centred, so it starts at x=101 and
	// its frame (10 pixels in) is at about x=116 to 124.
	if gray(out, 5, 700) < 250 || gray(out, 1066, 700) < 250 {
		t.Error("the padding at the sides must be paper white")
	}
	if gray(out, 120, 700) > 60 || gray(out, 951, 700) > 60 {
		t.Errorf("the page's frame must be fully visible (nothing cut off): %d %d", gray(out, 120, 700), gray(out, 951, 700))
	}
	if gray(out, 536, 18) > 60 || gray(out, 536, 1429) > 60 {
		t.Error("the page's top and bottom frame must be there, as the page fills the screen's height")
	}
}

func TestSmartFillsTheScreenWhenThePageIsAlmostTheRightShape(t *testing.T) {
	page := framed(700, 950, 255, 6) // 0.7368 against 0.7403
	out := Apply(page, Options{Profile: pw3, Mode: Smart})
	// Stretched to the whole screen: the frame sits 10 pixels in, about 15 after scaling.
	if gray(out, 20, 700) > 60 || gray(out, 1051, 700) > 60 || gray(out, 536, 20) > 60 {
		t.Error("a nearly matching page must fill the screen, so its frame is near the screen's edges")
	}

	far := framed(600, 1000, 255, 6) // 0.6: too narrow to stretch
	padded := Apply(far, Options{Profile: pw3, Mode: Smart})
	if gray(padded, 20, 700) < 250 {
		t.Error("a page of another shape must keep its shape and be padded, not stretched")
	}
	custom := Apply(far, Options{Profile: pw3, Mode: Smart, MaxStretch: 0.3})
	if gray(custom, 22, 700) > 60 {
		t.Error("a larger MaxStretch lets the same page fill the screen")
	}
}

func TestCoverFillsTheScreenAndCutsWhatSticksOut(t *testing.T) {
	out := Apply(framed(600, 1000, 255, 6), Options{Profile: pw3, Mode: Cover})
	if b := out.Bounds(); b.Dx() != 1072 || b.Dy() != 1448 {
		t.Fatalf("size = %v", b)
	}
	// Scaled by 1.787 and cut 169 pixels at the top and bottom: the top frame
	// (at about y=18 to 29) is gone, the side frame (x=18 to 29) is kept.
	if gray(out, 536, 22) < 250 {
		t.Error("cover mode cuts the top and bottom of a narrow page, so the top frame is gone")
	}
	if gray(out, 22, 700) > 60 {
		t.Error("cover mode keeps the full width")
	}
}

func TestPaddingTakesThePaperColourOfThePage(t *testing.T) {
	out := Apply(framed(600, 1000, 200, 6), Options{Profile: pw3, Mode: Fit})
	if v := gray(out, 5, 700); v < 196 || v > 204 {
		t.Errorf("padding = %d, want the page's own grey of 200", v)
	}
}

func TestEnhanceMakesPaperWhiteAndInkBlack(t *testing.T) {
	page := image.NewGray(image.Rect(0, 0, 100, 100))
	for i := range page.Pix {
		page.Pix[i] = 228 // yellowed paper
	}
	for y := 40; y < 60; y++ { // a line of ink
		for x := 10; x < 90; x++ {
			page.SetGray(x, y, color.Gray{30})
		}
	}
	out := Apply(page, Options{Enhance: true})
	if gray(out, 5, 5) < 250 {
		t.Errorf("paper = %d, want white", gray(out, 5, 5))
	}
	if gray(out, 50, 50) > 20 {
		t.Errorf("ink = %d, want close to black", gray(out, 50, 50))
	}
	if _, ok := out.(*image.Gray); !ok {
		t.Errorf("a page is gray after enhancing, got %T", out)
	}
}

func TestNothingIsChangedWithoutOptions(t *testing.T) {
	page := framed(10, 10, 255, 1)
	if Apply(page, Options{}) != image.Image(page) {
		t.Error("zero options must return the page itself")
	}
}

func TestRotateTurnsAQuarterClockwise(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 3, 2)) // 3 wide, 2 high
	src.SetGray(0, 0, color.Gray{10})            // top-left
	src.SetGray(2, 1, color.Gray{20})            // bottom-right
	out := Rotate(src)
	if b := out.Bounds(); b.Dx() != 2 || b.Dy() != 3 {
		t.Fatalf("size = %v, want 2x3", b)
	}
	// Clockwise: the top-left corner moves to the top-right, the bottom-right to the bottom-left.
	if gray(out, 1, 0) != 10 || gray(out, 0, 2) != 20 {
		t.Errorf("corners: %d %d", gray(out, 1, 0), gray(out, 0, 2))
	}
	rgba := image.NewRGBA(image.Rect(0, 0, 3, 2))
	rgba.Set(0, 0, color.RGBA{255, 0, 0, 255})
	if r, _, _, _ := Rotate(rgba).At(1, 0).RGBA(); r>>8 != 255 {
		t.Error("colour pictures rotate too")
	}
}

func TestPaddingUsesTheGivenPaperColourEvenWhenTheEdgeIsInk(t *testing.T) {
	// A cropped page: its edge is the black frame, but the original margin was white.
	page := image.NewGray(image.Rect(0, 0, 600, 1000))
	for i := range page.Pix {
		page.Pix[i] = 0
	}
	withPaper := Apply(page, Options{Profile: pw3, Mode: Fit, Paper: color.White})
	if gray(withPaper, 5, 700) < 250 {
		t.Errorf("padding = %d, want the paper colour that was given", gray(withPaper, 5, 700))
	}
	byEdge := Apply(page, Options{Profile: pw3, Mode: Fit})
	if gray(byEdge, 5, 700) > 5 {
		t.Errorf("without a paper colour the black edge is used: %d", gray(byEdge, 5, 700))
	}
	// Enhancing turns light paper white: padding must match the enhanced page.
	grey := framed(600, 1000, 228, 6)
	enhanced := Apply(grey, Options{Profile: pw3, Mode: Fit, Enhance: true, Paper: color.Gray{228}})
	if gray(enhanced, 5, 700) < 250 {
		t.Errorf("padding next to an enhanced page = %d, want white", gray(enhanced, 5, 700))
	}
}
