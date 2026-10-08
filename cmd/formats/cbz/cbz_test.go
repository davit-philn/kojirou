package cbz

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"image"
	"io"
	"testing"

	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

func testManga() md.Manga {
	page := func() image.Image { return image.NewGray(image.Rect(0, 0, 4, 4)) }
	chapter := md.Chapter{
		Info: md.ChapterInfo{
			Identifier:       md.NewIdentifier("1"),
			VolumeIdentifier: md.NewIdentifier("1"),
			Language:         language.English,
		},
		Pages: map[int]image.Image{2: page(), 1: page()},
	}
	volume := md.Volume{
		Info:     md.VolumeInfo{Identifier: md.NewIdentifier("1")},
		Chapters: map[md.Identifier]md.Chapter{md.NewIdentifier("1"): chapter},
		Cover:    page(),
	}

	return md.Manga{
		Info:    md.MangaInfo{Title: "Test Series"},
		Volumes: map[md.Identifier]md.Volume{md.NewIdentifier("1"): volume},
	}
}

func readZip(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close() //nolint:errcheck
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = b
	}

	return files
}

func TestGenerate(t *testing.T) {
	buf := new(bytes.Buffer)
	if err := Generate(buf, testManga(), Options{RightToLeft: true}); err != nil {
		t.Fatal(err)
	}

	files := readZip(t, buf.Bytes())
	for _, name := range []string{"00000.jpg", "00001.jpg", "00002.jpg", "ComicInfo.xml"} {
		if _, ok := files[name]; !ok {
			t.Errorf("missing archive entry %q", name)
		}
	}
	if len(files) != 4 {
		t.Errorf("got %d entries, want 4 (cover, 2 pages, ComicInfo.xml)", len(files))
	}

	var info comicBookInfo
	if err := xml.Unmarshal(files["ComicInfo.xml"], &info); err != nil {
		t.Fatal(err)
	}
	if info.Series != "Test Series" || info.Number != "1" || info.PageCount != 3 {
		t.Errorf("unexpected ComicInfo: %+v", info)
	}
	if info.Manga != "YesAndRightToLeft" || info.LanguageISO != "en" {
		t.Errorf("unexpected reading direction or language: %+v", info)
	}
	if len(info.Pages) != 3 || info.Pages[0].Type != "FrontCover" {
		t.Errorf("first page should be the front cover: %+v", info.Pages)
	}
}

func TestGenerateProcessSplitsPages(t *testing.T) {
	double := func(img image.Image) []image.Image { return []image.Image{img, img} }
	buf := new(bytes.Buffer)
	if err := Generate(buf, testManga(), Options{Process: double}); err != nil {
		t.Fatal(err)
	}

	// cover is not processed; each of the 2 pages becomes 2 images
	if got := len(readZip(t, buf.Bytes())); got != 1+4+1 {
		t.Errorf("got %d entries, want 6", got)
	}
}

func TestGenerateEncoding(t *testing.T) {
	tests := []struct {
		name  string
		opts  Options
		ext   string
		magic []byte
	}{
		{"default jpeg", Options{}, ".jpg", []byte{0xff, 0xd8}},
		{"custom quality", Options{JPEGQuality: 30}, ".jpg", []byte{0xff, 0xd8}},
		{"lossless png", Options{Lossless: true, JPEGQuality: 30}, ".png", []byte("\x89PNG")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			if err := Generate(buf, testManga(), tt.opts); err != nil {
				t.Fatal(err)
			}
			data, ok := readZip(t, buf.Bytes())["00001"+tt.ext]
			if !ok {
				t.Fatalf("missing 00001%s", tt.ext)
			}
			if !bytes.HasPrefix(data, tt.magic) {
				t.Errorf("wrong image signature: % x", data[:4])
			}
		})
	}
}

func TestGenerateQualityAffectsSize(t *testing.T) {
	size := func(q int) int {
		buf := new(bytes.Buffer)
		if err := Generate(buf, noisyManga(), Options{JPEGQuality: q}); err != nil {
			t.Fatal(err)
		}
		return buf.Len()
	}
	if low, high := size(10), size(95); low >= high {
		t.Errorf("quality 10 (%d bytes) should be smaller than quality 95 (%d bytes)", low, high)
	}
}

func TestGenerateInvalidQuality(t *testing.T) {
	for _, q := range []int{-1, 101} {
		if err := Generate(new(bytes.Buffer), testManga(), Options{JPEGQuality: q}); err == nil {
			t.Errorf("quality %d should be rejected", q)
		}
	}
}

func noisyManga() md.Manga {
	img := image.NewGray(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7919 % 251)
	}
	m := testManga()
	for id, vol := range m.Volumes {
		vol.Cover = img
		m.Volumes[id] = vol
	}

	return m
}

func TestFilename(t *testing.T) {
	if got := filename(md.NewIdentifier("3")); got != "0003.cbz" {
		t.Errorf("got %q", got)
	}
	if got := filename(md.NewIdentifier("3.5")); got != "0003.05.cbz" {
		t.Errorf("got %q", got)
	}
}
