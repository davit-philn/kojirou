package kindle

import (
	"image"
	"image/color"
	"testing"

	"github.com/leotaku/kojirou/cmd/device"
)

// page makes a w×h white page with a black frame 20 pixels in from its edges.
func page(w, h int) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, w, h))
	for i := range g.Pix {
		g.Pix[i] = 255
	}
	for y := 20; y < h-20; y++ {
		for x := 20; x < w-20; x++ {
			if x < 30 || y < 30 || x >= w-30 || y >= h-30 {
				g.SetGray(x, y, color.Gray{0})
			}
		}
	}

	return g
}

func grayAt(img image.Image, x, y int) uint8 {
	return color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y
}

func TestPaddingStaysWhiteAfterCroppingTheMargins(t *testing.T) {
	pw3, _ := device.Lookup("kindle-pw3")
	out := ProcessPage(page(600, 1000), PageOptions{
		Autocrop: true,
		Device:   device.Options{Profile: pw3, Mode: device.Fit},
	})
	if len(out) != 1 || out[0].Bounds().Dx() != 1072 || out[0].Bounds().Dy() != 1448 {
		t.Fatalf("got %d pages of %v, want one of 1072x1448", len(out), out[0].Bounds())
	}
	// Cropping leaves the black frame at the page's edge; the padding must still be the white margin.
	if grayAt(out[0], 4, 700) < 250 {
		t.Errorf("padding next to a cropped page is %d, want the white of the original margin", grayAt(out[0], 4, 700))
	}
}

func TestWidePagePolicies(t *testing.T) {
	wide := page(1400, 950)
	if got := len(ProcessPage(wide, PageOptions{Widepage: WidepagePolicySplit})); got != 2 {
		t.Errorf("split gave %d pages, want 2", got)
	}
	rotated := ProcessPage(wide, PageOptions{Widepage: WidepagePolicyRotate})
	if len(rotated) != 1 || rotated[0].Bounds().Dx() != 950 || rotated[0].Bounds().Dy() != 1400 {
		t.Errorf("rotate gave %d pages of %v, want one of 950x1400", len(rotated), rotated[0].Bounds())
	}
	if got := ProcessPage(page(600, 1000), PageOptions{Widepage: WidepagePolicyRotate}); got[0].Bounds().Dx() != 600 {
		t.Error("a page that is not wide must not be rotated")
	}
	same := page(600, 1000)
	if got := ProcessPage(same, PageOptions{}); len(got) != 1 || got[0] != image.Image(same) {
		t.Error("without options a page is returned as it is")
	}
}
