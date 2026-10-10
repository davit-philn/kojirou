package cmd

import (
	"context"
	"fmt"

	"github.com/leotaku/kojirou/cmd/formats"
	"github.com/leotaku/kojirou/cmd/formats/download"
	"github.com/leotaku/kojirou/cmd/formats/kindle"
	"github.com/leotaku/kojirou/cmd/formats/selector"
	"github.com/leotaku/kojirou/cmd/job"
	md "github.com/leotaku/kojirou/mangadex"
)

func run(ctx context.Context) error {
	opts, err := optionsFromFlags()
	if err != nil {
		return err
	}

	_, err = job.Run(ctx, opts, cliReporter{})
	return err
}

func optionsFromFlags() (job.Options, error) {
	// job treats 0 as "use the default"; on the command line it is an error.
	if jpegQualityArg < 1 || jpegQualityArg > 100 {
		return job.Options{}, fmt.Errorf("--jpeg-quality must be between 1 and 100")
	}

	opts := job.Options{
		Identifier:       identifierArg,
		Language:         languageArg,
		Rank:             rankArg,
		Groups:           groupsFilter,
		Volumes:          volumesFilter,
		Chapters:         chaptersFilter,
		DryRun:           dryRunArg,
		Force:            forceArg,
		Disk:             diskArg,
		Format:           job.Format(formatArg),
		Autocrop:         autocropArg,
		Widepage:         kindle.WidepagePolicy(widepageArg),
		LeftToRight:      leftToRightArg,
		FillVolumeNumber: fillVolumeNumberArg,
		DataSaver:        download.DataSaverPolicy(dataSaverArg),
		JPEGQuality:      jpegQualityArg,
		Lossless:         losslessArg,
		Out:              outArg,
		KindleFolderMode: kindleFolderModeArg,
	}

	if sourceConfigArg != "" {
		cfg, err := selector.LoadConfig(sourceConfigArg)
		if err != nil {
			return opts, fmt.Errorf("source: source config %q: %w", sourceConfigArg, err)
		}
		opts.Source = &cfg
	}

	return opts, nil
}

// cliReporter shows progress bars on the terminal.
type cliReporter struct{}

func (cliReporter) Summary(manga *md.Manga) {
	formats.PrintSummary(manga)
}

func (cliReporter) Task(title string, vanishing bool) job.Progress {
	var p formats.CliProgress
	if vanishing {
		p = formats.VanishingProgress(title)
	} else {
		p = formats.TitledProgress(title)
	}

	return &cliProgress{p}
}

type cliProgress struct {
	formats.CliProgress
}

func (p *cliProgress) Cancel(message string) {
	p.CliProgress.Cancel(message)
}
