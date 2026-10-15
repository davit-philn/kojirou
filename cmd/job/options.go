// Package job runs the download pipeline: fetch series metadata and chapters
// from a source, download pages, and write volumes as e-books. It is
// independent of the command line so that it can also back a web interface.
package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/leotaku/kojirou/cmd/formats"
	"github.com/leotaku/kojirou/cmd/formats/download"
	"github.com/leotaku/kojirou/cmd/formats/kindle"
	"github.com/leotaku/kojirou/cmd/formats/selector"
	md "github.com/leotaku/kojirou/mangadex"
)

type Format string

const (
	FormatMOBI Format = "mobi"
	FormatCBZ  Format = "cbz"
)

// Options mirrors the command line flags.
type Options struct {
	// Identifier is a MangaDex series ID, or the series page URL when Source is set.
	Identifier string
	// Source selects a CSS-selector source instead of MangaDex.
	Source *selector.SourceConfig
	// Fill adds the chapters MangaDex lacks from a second website. MangaDex
	// stays the main source for volumes and covers. Not combinable with Source.
	Fill *FillSource

	// VolumesFrom is a MangaDex series ID. With Source set, the chapters
	// come from the website while MangaDex decides the volumes and supplies
	// the series' metadata and covers.
	VolumesFrom string

	Language string
	Rank     string

	// Filters; empty means no filtering.
	Groups   string
	Volumes  string
	Chapters string
	// ChapterIDs restricts the download to these chapter IDs (md.ChapterInfo.ID).
	ChapterIDs []string

	DryRun bool
	Force  bool
	Disk   string

	// Interactive makes MangaDex lookups fail fast instead of retrying for
	// a long time. Use it when a person is waiting for the result.
	Interactive bool

	Format           Format
	Autocrop         bool
	Widepage         kindle.WidepagePolicy
	LeftToRight      bool
	FillVolumeNumber int
	DataSaver        download.DataSaverPolicy
	JPEGQuality      int
	Lossless         bool

	// Out is the output directory. If empty, a directory named after the
	// series is used, inside LibraryDir when that is set.
	Out string
	// LibraryDir, if set, makes the output go to LibraryDir/<series title>.
	LibraryDir       string
	KindleFolderMode bool
}

func (o Options) validate() error {
	switch o.Format {
	case FormatMOBI, FormatCBZ:
	default:
		return fmt.Errorf("unknown format %q", o.Format)
	}
	if o.Format == FormatCBZ && o.KindleFolderMode {
		return errors.New("--kindle-folder-mode is not supported with --format=cbz")
	}
	if o.JPEGQuality < 0 || o.JPEGQuality > 100 {
		return errors.New("--jpeg-quality must be between 1 and 100")
	}
	if o.Identifier == "" {
		return errors.New("no series identifier given")
	}
	if o.VolumesFrom != "" {
		if o.Source == nil {
			return errors.New("taking volumes from MangaDex needs a website as the main source")
		}
		if o.Fill != nil {
			return errors.New("a fill source cannot be combined with volumes from MangaDex")
		}
	}
	if o.Fill != nil {
		if o.Source != nil {
			return errors.New("a fill source needs MangaDex as the main source")
		}
		if u, err := url.Parse(o.Fill.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("the fill source needs the series page as an http(s) URL")
		}
		if _, err := selector.ParseConfig(mustJSON(o.Fill.Config)); err != nil {
			return fmt.Errorf("fill source: %w", err)
		}
	}

	return nil
}

func mustJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

// Progress reports the progress of one step.
type Progress interface {
	formats.Progress
	Done()
	Cancel(message string)
}

// Reporter receives progress and results while a job runs.
type Reporter interface {
	// Summary is called once the chapters to download are known.
	Summary(manga *md.Manga)
	// Task starts a new step. Vanishing steps are short and not worth keeping.
	Task(title string, vanishing bool) Progress
}

// Result describes a finished job.
type Result struct {
	Title string
	// Dir is where volumes were written; empty for dry runs.
	Dir string
	// Notes lists decisions that may need a look, e.g. guessed volumes.
	Notes []string
}

// NopReporter discards all progress.
type NopReporter struct{}

func (NopReporter) Summary(*md.Manga)          {}
func (NopReporter) Task(string, bool) Progress { return nopProgress{} }
