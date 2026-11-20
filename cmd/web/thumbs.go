package web

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	thumbWidth      = 240
	maxCoverEntry   = 40 << 20
	maxThumbsCached = 400
)

// thumbCache keeps the thumbnails made so far, so that opening the library
// again does not decode every archive again.
type thumbCache struct {
	mu    sync.Mutex
	items map[string][]byte
	order []string
}

func newThumbCache() *thumbCache { return &thumbCache{items: map[string][]byte{}} }

func (c *thumbCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, ok := c.items[key]

	return data, ok
}

func (c *thumbCache) put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok {
		c.order = append(c.order, key)
		if len(c.order) > maxThumbsCached {
			delete(c.items, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.items[key] = data
}

// handleLibraryCover serves a small JPEG of the cover of a CBZ in the library:
// the page ComicInfo.xml marks as the front cover, else the first picture.
func (s *Server) handleLibraryCover(w http.ResponseWriter, r *http.Request) {
	series, file := r.PathValue("series"), r.PathValue("file")
	path, ok := libraryFilePath(s.cfg.LibraryDir, series, file)
	if !ok || !strings.EqualFold(filepath.Ext(file), ".cbz") {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	key := fmt.Sprintf("%s|%d|%d", path, info.ModTime().UnixNano(), info.Size())
	data, ok := s.thumbs.get(key)
	if !ok {
		if data, err = coverThumbnail(path, thumbWidth); err != nil {
			http.NotFound(w, r)
			return
		}
		s.thumbs.put(key, data)
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

type comicInfoPages struct {
	Pages []struct {
		Image int    `xml:"Image,attr"`
		Type  string `xml:"Type,attr"`
	} `xml:"Pages>Page"`
}

// coverThumbnail reads the cover of a comic archive and scales it to the
// given width.
func coverThumbnail(path string, width int) ([]byte, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close() //nolint:errcheck

	var images []*zip.File
	var info *zip.File
	for _, f := range zr.File {
		switch ext := strings.ToLower(filepath.Ext(f.Name)); {
		case strings.EqualFold(f.Name, "ComicInfo.xml"):
			info = f
		case ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif":
			images = append(images, f)
		}
	}
	if len(images) == 0 {
		return nil, errors.New("no pictures in the archive")
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Name < images[j].Name })

	cover := images[0]
	if info != nil && info.UncompressedSize64 <= 1<<20 {
		if rc, err := info.Open(); err == nil {
			var meta comicInfoPages
			if err := xml.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&meta); err == nil {
				for _, p := range meta.Pages {
					if p.Type == "FrontCover" && p.Image >= 0 && p.Image < len(images) {
						cover = images[p.Image]
						break
					}
				}
			}
			rc.Close() //nolint:errcheck
		}
	}
	if cover.UncompressedSize64 > maxCoverEntry {
		return nil, errors.New("cover picture too large")
	}
	rc, err := cover.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close() //nolint:errcheck
	img, _, err := image.Decode(io.LimitReader(rc, maxCoverEntry))
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, scaleToWidth(img, width), &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

// scaleToWidth shrinks a picture to the given width by averaging the source
// pixels each new pixel covers; smaller pictures are kept as they are.
func scaleToWidth(src image.Image, width int) image.Image {
	b := src.Bounds()
	if b.Dx() <= width || b.Dx() == 0 || b.Dy() == 0 {
		rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)

		return rgba
	}
	height := b.Dy() * width / b.Dx()
	if height < 1 {
		height = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		y0, y1 := b.Min.Y+y*b.Dy()/height, b.Min.Y+(y+1)*b.Dy()/height
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < width; x++ {
			x0, x1 := b.Min.X+x*b.Dx()/width, b.Min.X+(x+1)*b.Dx()/width
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r, g, bl, n = r+cr>>8, g+cg>>8, bl+cb>>8, n+1
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/n), uint8(g/n), uint8(bl/n), 255
		}
	}

	return dst
}
