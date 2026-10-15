<a href="https://mangadex.org/title/22631"><img src="./.github/header.jpg" alt="Header Image" width="80%"></a>

<h1>
  <span>Kojirou</span>
  <a href="https://goreportcard.com/report/github.com/leotaku/kojirou">
    <img src="https://goreportcard.com/badge/github.com/leotaku/kojirou?style=flat-square" alt="Go Report Card">
  </a>
  <a href="https://github.com/leotaku/kojirou/actions">
    <img src="https://img.shields.io/github/actions/workflow/status/leotaku/kojirou/check.yml?branch=master&label=check&logo=github&logoColor=white&style=flat-square" alt="Github CI Status">
  </a>
  <a href="https://github.com/leotaku/kojirou/wiki/Home">
    <img src="https://img.shields.io/github/actions/workflow/status/leotaku/kojirou/wiki.yml?branch=master&label=wiki&color=blue&logo=gitbook&logoColor=white&style=flat-square" alt="GitHub Wiki Status">
  <a/>
</h1>

> Generate perfectly formatted Kindle e-books from MangaDex manga

## Features

### Download manga and generate Kindle e-books

Kojirou will automatically download the series for the specified ID and language while outputting a folder with all the downloaded volumes.

``` shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en
```

### Generate Kindle folder structure for easy synchronization

Kojirou can also output a folder structure matching that of any modern Kindle device to allow for easy synchronization using e.g. rsync.

``` shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --kindle-folder-mode
udisksctl mount -b /dev/sdb
rsync kindle/ /run/media/user/Kindle/
```

### Customize ranking for better scantlations

Kojirou has the ability to use different [ranking algorithms](https://github.com/leotaku/kojirou/wiki/Ranking) in order to always download the highest-quality scantlations.
You can preview what would be downloaded by running in dry-run mode.

``` shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --rank newest --dry-run
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --rank most
```

### Load chapters from the filesystem

Kojirou has the ability to load chapters from your local filesystem.
This can be useful if certain chapters are not available on MangaDex, or you want to convert your existing collection.
Chapters found locally are always preferred, even if they are also available on MangaDex.

``` shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --disk /path/to/directory
```

The directory structure should follow the following pattern.
Sorting of volumes, chapters and pages is done numerically and an arbitrary number of leading zeros is supported.

+ `root/`
  + `01/` :: Volume
    + `cover.{jpeg,jpg,png,bmp}` :: Volume cover (optional)
    + `01: Title/` :: Chapter (with optional title, use colon ":")
      + `01.{jpeg,jpg,png,bmp}` :: Page

### Export CBZ instead of Kindle e-books

Kojirou can also write each volume as a CBZ comic book archive, readable by most e-reader apps such as Komga, Kavita, KOReader and Tachiyomi.
Pages are encoded as JPEG (quality 90 by default) and a `ComicInfo.xml` with title, authors, language and reading direction is included.
Options like `--autocrop`, `--widepage` and `--left-to-right` apply as usual, while `--kindle-folder-mode` is not supported with this format.

``` shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --format cbz
```

Use `--jpeg-quality` (1-100) to trade size for quality, or `--lossless` to store pages as PNG instead.
Both options only affect the CBZ format.

``` shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --format cbz --jpeg-quality 75
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --format cbz --lossless
```

### Crop whitespace from pages automatically

Kojirou has the ability to crop whitespace from the borders of manga pages.
This may be useful if your e-reader has a small screen.

``` shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --autocrop
```

### Split wide pages automatically

Kojirou has the ability to split panorama pages into two separate pages for better viewing.
It is also possible to include both the split pages and the original page.
Legal arguments to this option are "preserve", "split", "preserve-and-split" and "split-and-preserve".

``` shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --widepage=preserve-and-split
```

### Change reading direction

Kojirou, by default, generates e-books with right-to-left reading direction, as this is the default convention for most manga.
Also note that right-to-left reading does not seem to be supported on all Kindle devices.

``` shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --left-to-right
```

### Fill volume number in title

Kojirou has the ability to fill the volume number in e-book titles with an arbitrary number of leading zeros.
This is useful because Kindle devices sort titles alphabetically without any special handling of numbers.
So, for example, volume "2" would be placed before "10", while "02" would be correctly sorted.

```shell
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --fill-volume-number 2
```

### Use lower quality images to save space

Kojirou has the ability to download lower-quality images from MangaDex.
This can be useful to save space on your device, or to reduce the amount of data downloaded on slow or limited connections.
Legal arguments to this option are "no", "prefer" and "fallback".

```
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --data-saver=prefer
```

### Fallback to lower quality alternatives for broken images

MangaDex sometimes hosts images that are subtly broken and cannot be reliably converted to an image format compatible with Kindle devices.
Kojirou can be configured to fall back on reencoded lower-quality versions of these images, which often do not have the same problems.

```
kojirou d86cf65b-5f6c-437d-a0af-19a31f94ec55 -l en --data-saver=fallback
```

## Web interface

Kojirou also comes with a local web interface, so you can pick series with the mouse instead of typing commands.
Start it with `kojirou serve`, or simply run the program without any arguments (for example by double-clicking it), and your browser opens automatically.

``` shell
kojirou serve                           # http://127.0.0.1:8080, downloads go to ./library
kojirou serve --library D:\Manga --addr 127.0.0.1:9000
```

+ **Duyệt truyện** :: Browse popular or latest series on MangaDex, search by title and filter by translation language
+ **Chapter picker** :: Open a series, tick whole volumes or single chapters (or a range) and choose the format and image options
+ **Đang tải** :: Watch queued downloads, with progress per volume, and cancel them
+ **Thư viện** :: Download finished volumes from the library folder again through the browser
+ **Nguồn** :: Add websites described by CSS selectors (see below), test the selectors on a real page, then browse them like MangaDex
+ **Fill from another site** :: On a MangaDex series page, pick a saved source and the series page on that site to fill the chapters MangaDex lacks (see below); the link is remembered for next time

The server has no login and only listens on your own computer by default.
It rejects requests with an unexpected `Host` or `Origin` header and requires a custom header for every change, so other websites open in your browser cannot control it.
Do not expose it to a network you do not trust.
Saved sources live in your user configuration directory (`--config-dir` changes it).
To look at the interface without internet access, run `python3 contrib/mock_mangadex.py 9100` and start the server with `--api-url http://127.0.0.1:9100/ --covers-url http://127.0.0.1:9100/covers/`.

## Fill missing chapters from a second website

MangaDex often lacks chapters.
With `--fill-config` and `--fill-url`, chapters that MangaDex does not have are taken from a second website, while MangaDex stays the main source: its chapters win, and its volumes and covers are used.
Chapters are matched by chapter number.
A chapter that only the second website has goes into the volume of its neighbours, e.g. chapter 18 between two chapters of volume 3 goes into volume 3.
If its neighbours belong to different volumes the earlier volume is used and a note is printed, and chapters newer than everything on MangaDex go to a separate `Special` volume.

``` shell
kojirou https://mangadex.org/title/<id>/<slug> -l vi --format cbz \
  --fill-config other-site.json --fill-url https://other-site.example/series/my-series
```

The configuration file is the same as for custom sources below, for example:

``` json
{
  "base_url": "https://other-site.example",
  "chapter_list_selector": ".works-chapter-item .name-chap a",
  "image_list_selector": ".story-see-content img",
  "image_attr": "src"
}
```

Only use websites whose terms allow automated access and whose content you have the right to use.
The option cannot be combined with `--source-config`.

In the web interface the same thing is done on the series page of a MangaDex series, in the panel *Bù chương thiếu từ trang khác*: choose a source saved in the *Nguồn* tab, paste the series page URL on that site and click *Áp dụng và lưu liên kết*.
Chapters taken from the other site are marked *nguồn bù*, and guessed volumes are listed under *Cần kiểm tra*.
Links are stored in `links.json` next to `sources.json` in the configuration directory.

## Automatic source detection

In the web interface, **Nguồn → Tự nhận diện từ trang chủ** takes only a website's home page.
After you confirm that you may use the site's content, Kojirou reads `robots.txt`, opens a few pages of that site (at most 8, same host only, with a pause between requests), finds the chapter list and the page images, builds the selectors itself and saves the result as a source.
If the titles contain `Tập N` / `Vol N` the volume rule is filled in too; otherwise the notes say how to add one.
Detected selectors can be generic on small sites; use **Thử** to check them and edit them if needed.
Pages blocked by `robots.txt`, sites that need JavaScript, logins or browser checks cannot be scanned.
The same logic is available from Go as `selector.Detect`.

## Volumes from MangaDex for a custom source

When a website has the chapters but no volumes, MangaDex can tell which chapter belongs to which volume.
On the series page of a custom source open **Chia tập theo MangaDex**, search MangaDex by title (or paste its link) and choose the series.
Chapters and images still come from the website; MangaDex only supplies the volume of each chapter number, the title, authors and the covers.
MangaDex often has no volume for most chapters of a long series, so the series title is also looked up on Wikipedia ("List of … chapters"), whose volume tables are complete for well-known series; where both know a chapter, MangaDex wins because the covers are filed under its volume numbers.
This sends the series title to the Wikipedia API; the page it used is shown above the chapter list.
Chapters neither knows take the volume of their neighbours (guesses are listed under "Cần kiểm tra"), and chapters newer than every volume go into `Special`.
The choice is remembered in the browser for that series page.
Nothing is uploaded to MangaDex.

``` shell
kojirou https://example.org/series/my-series -l vi --source-config source.json --volumes-from https://mangadex.org/title/<id>
```

## Custom sources

Besides MangaDex, Kojirou can read chapters from a website described by a JSON file of CSS selectors.
Pass the file with `--source-config`; the `<identifier>` argument is then the URL of the series page that lists the chapters.
Only point this at sites whose terms allow automated access.

``` json
{
  "base_url": "https://example.org",
  "user_agent": "kojirou",
  "referer": "https://example.org/",
  "chapter_list_selector": ".chapters a",
  "image_list_selector": "#reader img",
  "image_attr": "data-original",
  "title": "My Series",
  "chapters_per_volume": 20,
  "max_concurrent_downloads": 4
}
```

``` shell
kojirou https://example.org/series/my-series -l en --source-config source.json --format cbz
```

+ `chapter_list_selector` :: Selects one element per chapter, which must be (or contain) a link
+ `image_list_selector` :: Selects one element per page image on a chapter page
+ `image_attr` :: Attribute holding the image URL, e.g. `src` (default) or `data-original`; several can be listed, separated by commas, and the first one holding an http(s) address is used (handy for lazy-loading pages that keep a placeholder in `src`)
+ `title` :: Series title for output files (default: last part of the series URL)
+ `volume_ranges` :: Assign volumes by chapter number, as `volume:first-last` items, e.g. `"1:1-8, 2:9-17, 3:18-26"` (tried first)
+ `volume_regex` :: Take the volume from the chapter title or URL; the first capture group is the volume, e.g. `"Tập\\s*(\\d+)"` (tried after `volume_ranges`)
+ `chapters_per_volume` :: Group the chapters that no rule above matched into volumes of this size
+ `max_concurrent_downloads` :: Parallel image downloads, 1 to 8 (default: 4)
+ `timeout_seconds` :: Timeout per request (default: 30)

Chapter numbers are taken from the number after a chapter keyword (`Chương`, `Chapter`, `Ch`, `Ep`...), or else from the first number in the title.

Without any volume rule every chapter goes into volume 1.
If the website does not tell the volumes, split them yourself with `volume_ranges` (for example following the official release list), or let `volume_regex` read them from titles that contain the volume.
When a volume rule is set, chapters that no rule matches go into a separate `Special` volume unless `chapters_per_volume` is set.
Chapters taken from a second site with `--fill-config` ignore these rules: they join the volume of their neighbours on MangaDex.
Pages are downloaded a few at a time and retried with backoff on HTTP 429 and 5xx responses.
To try it without a real website, run `python3 contrib/selector_demo.py` and follow the command it prints.
If a selector finds nothing, the error says what the page contained (its title, how many links and images it has, which attributes the matched elements carry) and whether it looks like a browser-check page, which this tool cannot pass.
Only plain HTML is read, so sites that load images with JavaScript are not supported, and there are no covers.
`--data-saver` has no effect on custom sources.

## Prebuilt binaries

Prebuilt binaries for Linux, Windows and MacOS on x86 and ARM processors are provided.
Visit the [release tab](https://github.com/leotaku/kojirou/releases) to download the archive for your respective setup.

On Linux and MacOS you will have to make the provided binary executable after extracting it from the archive.

``` shell
chmod u+x ./kojirou.exe
```

Afterwards, verify your installation succeeded by executing the application on the command line.

``` shell
./kojirou.exe --version
```

## Install from source

Kojirou can be installed from source easily if you already have access to a Go toolchain.
Otherwise, follow the [Go installation instructions](https://go.dev/doc/install) for your operating system, then execute the following command.

``` shell
go install github.com/leotaku/kojirou@latest
```

Afterwards, verify your installation succeeded by executing the application on the command line.

``` shell
kojirou --version
```

On many systems, the Go binary directory is not added to the list of directories searched for executables by default.
If you get a "command not found" or similar error after the previous command, run the following command and try again.
If you are using Windows, please find out how to add directories to the lookup path yourself, as there does not seem to be any quality documentation that I could link here.

``` shell
export PATH="$PATH:$(go env GOPATH)/bin"
```

## License

[MIT](./LICENSE) © Leo Gaskin 2020-2026
