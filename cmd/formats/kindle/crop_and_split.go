package kindle

import (
	"image"

	"github.com/leotaku/kojirou/cmd/crop"
	"github.com/leotaku/kojirou/cmd/device"
)

type WidepagePolicy int

const (
	WidepagePolicyPreserve WidepagePolicy = iota
	WidepagePolicySplit
	WidepagePolicyPreserveAndSplit
	WidepagePolicySplitAndPreserve
	// WidepagePolicyRotate turns a wide page a quarter turn clockwise, so
	// that it can be read with the device held sideways.
	WidepagePolicyRotate
)

// PageOptions says how a page is prepared.
type PageOptions struct {
	Widepage WidepagePolicy
	Autocrop bool
	LTR      bool
	// Device brings pages to a screen's size and clarity; the zero value
	// leaves them as they are.
	Device device.Options
}

// CropAndSplit prepares a page without any device settings.
func CropAndSplit(img image.Image, widepage WidepagePolicy, autocrop bool, ltr bool) []image.Image {
	return ProcessPage(img, PageOptions{Widepage: widepage, Autocrop: autocrop, LTR: ltr})
}

// ProcessPage crops, splits or turns a page and then fits it to the device.
func ProcessPage(img image.Image, o PageOptions) []image.Image {
	dev := o.Device
	if dev.Profile.Width > 0 && dev.Paper == nil {
		// Padding takes the colour of the page's margin; after cropping, the
		// edge of the page is its content, not its margin.
		dev.Paper = device.PaperColor(img)
	}
	pages := cropAndSplit(img, o.Widepage, o.Autocrop, o.LTR)
	if dev.Active() {
		for i, page := range pages {
			pages[i] = device.Apply(page, dev)
		}
	}

	return pages
}

func cropAndSplit(img image.Image, widepage WidepagePolicy, autocrop bool, ltr bool) []image.Image {
	if autocrop {
		croppedImg, err := crop.Crop(img, crop.Bounds(img))
		if err != nil {
			panic("unsupported image type for splitting")
		}
		img = croppedImg
	}

	if widepage == WidepagePolicyRotate && crop.ShouldSplit(img) {
		return []image.Image{device.Rotate(img)}
	}
	if widepage != WidepagePolicyPreserve && crop.ShouldSplit(img) {
		left, right, err := crop.Split(img)
		if err != nil {
			panic("unsupported image type for splitting")
		}

		switch widepage {
		case WidepagePolicySplit:
			if ltr {
				return []image.Image{left, right}
			} else {
				return []image.Image{right, left}
			}
		case WidepagePolicyPreserveAndSplit:
			if ltr {
				return []image.Image{img, left, right}
			} else {
				return []image.Image{img, right, left}
			}
		case WidepagePolicySplitAndPreserve:
			if ltr {
				return []image.Image{left, right, img}
			} else {
				return []image.Image{right, left, img}
			}
		}
	}

	return []image.Image{img}
}
