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

+ **Tìm truyện** (the first page; browsing one source at a time is under *Duyệt riêng từng nguồn*) :: Browse popular or latest series on MangaDex, search by title and filter by translation language
+ **Chapter picker** :: Open a series, tick whole volumes or single chapters (or a range) and choose the format and image options
+ **Đang tải** :: Watch queued downloads, with progress per volume, and cancel them
+ **Thư viện** :: Shows each series with its cover and a thumbnail per volume (the picture `ComicInfo.xml` marks as the front cover, else the first one of the CBZ), and downloads finished volumes again through the browser
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
When the page shows a list of series, the source can also be browsed like MangaDex: **Tìm truyện → Duyệt riêng từng nguồn** then shows a grid of its series with covers, a "load more" button when the page has a next-page link, and a search box when the website has a GET search form. The settings it finds are `series_selector`, `next_page_selector`, `search_url` (with `{q}`) and `list_url`; covers are fetched through the local server with the website's Referer.
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
Chapters neither knows take the volume of their neighbours (guesses are listed under "Cần kiểm tra"), and chapters newer than every volume (or without a chapter number) go into the last volume. If MangaDex knows no volume at all they go into `Special`.
The choice is remembered in the browser for that series page.
Nothing is uploaded to MangaDex.

``` shell
kojirou https://example.org/series/my-series -l vi --source-config source.json --volumes-from https://mangadex.org/title/<id>
```

## Fitting pages to an e-reader (Kindle Paperwhite and others)

A reader's screen has its own shape (a Paperwhite 3 shows 1072×1448 pixels), and pages of other shapes end up with bars or cut off.
Choose a **Thiết bị đọc** in the download options (or `--device kindle-pw3`, `kindle-pw4`, `kindle-pw5`, `kindle-oasis`, `kindle-basic`, `kobo-clara`) and every page, and the cover, is brought to the screen's exact size, so the reader does not scale or cut anything:
+ `smart` (default, `--fit-mode smart`): a page whose shape is within 8% of the screen's is stretched slightly to fill it; any other page is fitted whole and padded
+ `fit`: the whole page is kept and padded
+ `cover`: the page fills the screen and what sticks out is cut off
Padding takes the colour of the page's own margin (before **Tự cắt viền trắng** crops it), so it does not show as a border.
**Làm rõ cho màn e-ink** (`--enhance`) makes pages gray, stretches the contrast so that the paper is white and the ink black, and sharpens lightly.
Wide pages can be split, kept, or turned a quarter turn (`--widepage rotate`).
**Xem trước 4 trang đầu của chương** in the download options shows the first pages of a chapter inside a frame like the reader's, before downloading.

In **Thư viện**, **Chuyển truyện cho máy đọc** does the same for files already downloaded: it previews pages of a series and writes a converted copy of every CBZ into a new series folder named `<series> [<device>]`; the originals are not touched.

## Cutting advertising strips and removing advertising pages

Scanlation websites often add a banner to the top of a chapter's first page and another to the bottom of its last page (the same picture every time).
For custom sources the download option **Tự cắt dải quảng cáo ở đầu/cuối chương** finds them by comparing the first and last pages of the chapters being downloaded: rows that look alike on several pages (compared at one width, so differently scaled copies still match) are a strip, and are cut off. Blank paper is never taken for a strip. The strips found are remembered per source in `ads.json` next to `sources.json`, so a later download of a single chapter is cleaned too. Sites also slip in whole advertising pages (a separate image, often of a different width, e.g. a banner of another site or a "visit our site" page): a page that looks like the same picture in several chapters, within the first or last 6 pages of a chapter, is dropped entirely. No OCR is used; pages are compared as pictures. An advert that appears in one file only cannot be found by comparison: in the scan result choose the file and page number and press **Đánh dấu là quảng cáo**; every page looking like it is then found and removed.
In **Thư viện**, **Dọn quảng cáo** (per series, or for all) scans every page of the downloaded files, shows the first page with a strip before and after cutting, and cuts them when you press **Cắt quảng cáo**; only the pages with an advert are rewritten, and whole advertising pages are removed (the following pages are renumbered and ComicInfo.xml is updated).
No text recognition (OCR) is used: it would need an extra program installed and is less reliable on banners printed over pictures than comparing the pictures.

## Updating covers of the library

In **Thư viện**, **Cập nhật ảnh bìa cho tất cả** (or **Cập nhật bìa** on one series) gives the CBZ files already in the library their proper cover, without downloading the chapters again.
For each series folder it finds the MangaDex series by name (only when a search result's title equals the folder name; otherwise it lists the candidates, and you can pick one, paste a MangaDex link, or paste a cover page such as a BookWalker series page), downloads the cover of every volume whose number is in the file name (`0001.cbz` is volume 1) and rewrites the archive: the cover becomes the first page, marked as the front cover in `ComicInfo.xml`, an existing front cover is replaced, all other pages stay as they are, and a missing series name, volume and title are filled in.
Every series also has a folded panel, **Dán link để lấy bìa**, to paste a MangaDex link or a cover page straight away without running the search first.
The chosen MangaDex series or cover page is remembered in `library.json` next to `sources.json`. Files written in the last 20 seconds are skipped. Only CBZ files are changed.

## Volume covers from another page

On a series page, **Bìa từng tập từ trang khác** takes the address of a page that lists a series' volumes with their covers, for example a BookWalker series page (`https://bookwalker.com/series/…/…`).
Kojirou reads that page (obeying `robots.txt`), finds a link per volume whose address ends in `vol-N` or `volume-N` and the cover image next to it, takes the widest picture the page offers, and uses it as the cover of volume N when downloading.
These covers replace the source's own covers and MangaDex's for the volumes they match; volumes the page does not show keep their other cover.
Only covers that are in the page's HTML can be read, and covers belong to their publisher: check the website's terms and use them for your own library.
**Xem thử** shows what was found before it is used. The choice is remembered per series in the browser and sent with the download (`coverPage`).

## Fixing volumes by hand

On any series page, **Chỉnh tập thủ công** lets you change how chapters are split into volumes before downloading:
split by chapter ranges (`1:1-8, 2:9-17`), split evenly (N chapters per volume from a starting volume), move the ticked chapters to a volume, or drag a chapter onto another volume's heading.
A volume is a number such as `3` or `3.5`, or `Special`; a new number creates a new volume.
The moves are remembered in the browser for that series and are sent with the download (`volumeOverrides`); "Khôi phục mặc định" undoes them.

## Opening a series by its address

At the top of **Tìm truyện** a box takes the address of a series: a MangaDex link opens it from MangaDex, and a link on the website of any saved source opens it with that source. For a website without a source it says so and points to the Nguồn tab.

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
Pages are downloaded a few at a time. Chapter pages are requested at least 400ms apart, and both chapter pages and images are retried with growing pauses on HTTP 429 and 5xx responses (honouring `Retry-After`); after a 429 every request slows down for a while. If a site still limits you, lower `max_concurrent_downloads` to 1 or 2.
To try it without a real website, run `python3 contrib/selector_demo.py` and follow the command it prints.
If a selector finds nothing, the error says what the page contained (its title, how many links and images it has, which attributes the matched elements carry) and whether it looks like a browser-check page, which this tool cannot pass.
Only plain HTML is read, so sites that load images with JavaScript are not supported. A source has no covers of its own: a series opened from the browse grid uses the cover shown there for every volume, and with volumes taken from MangaDex the covers are MangaDex's, one per volume.
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

Converting for a device never converts a converted copy (folders named `… [kindle-pw3]`); "Chuyển tất cả truyện" skips them.

Long operations of the library (cover update, device conversion, advert scan and cutting) show a progress bar with the file being worked on. "Làm rõ cho màn e-ink" is gentle and off by default; converting an already converted copy used to apply it again and again and made pages rough.

## Managing the library

The **Thư viện** page lists every series with its files and offers:

- search, sorting (name, newest, biggest) and the total size; converted copies (`… [kindle-pw3]`) are shown under their original;
- **Xem trang**: a grid of a file's pages; click the advertising pages and delete them, optionally remembering the picture so look-alike pages in other files are found by **Dọn quảng cáo**;
- **Xoá**, **Đổi tên** for files and series (renaming a series renames its converted copies too);
- **Backups**: before pages are removed or advertisements cut, the file is kept in `library/.backup/`; **Khôi phục** puts it back, **Xoá sao lưu** frees the space;
- **Tạo AZW3**: an `.azw3` next to each CBZ, for Kindles that cannot open CBZ (make the CBZ ready for the device first);
- **Chép sang máy đọc**: copies a series into a folder such as the Kindle's `E:\documents`;
- **Tập mới?**: for series downloaded with this version, opens the series page to download new volumes.

## Starting with automatic updates (Windows)

`contrib/chay-kojirou.bat` (copy it into the library folder, next to the series folders) starts Kojirou and, before that, looks on GitHub for a newer version of the branch. If there is one it downloads it, builds it with Go and replaces `kojirou.exe`; with no network or no Go it just starts the version it has. `chay-kojirou.bat --no-update` skips the check. The installed version is kept in `kojirou.version` next to `kojirou.exe`.

## Searching every source at once

The search box of **Tìm truyện** searches MangaDex and all saved sources that have a search address at the same time (a few requests in parallel, one slow or broken source never stops the others), groups the results that are the same series (titles are compared without case, accents, punctuation and bracketed notes) and, for the series you pick, lists the chapters of every chosen source in one table: which source has which chapter, the whole chapter numbers that **no** source has (warned in red), and what each source lacks between chapter 1 and its last one. Untick a source that found a different series.

### Downloading from several sources in one go

Under the chapter table, **Tải truyện từ các nguồn này** downloads the series as one: the sources are in an order of preference (move them with the arrows) and each chapter comes from the first source that has it; click a ✓ in the table to take that one chapter from another source (● marks the one in use). Below the table you choose which chapters (`1-50, 55`), how many chapters go into a volume (when MangaDex is not one of the sources — if it is, MangaDex decides the volumes), the Kindle PW3 preparation and the advert cutting. A source that cannot be read is skipped and reported; chapters that are not numbered are not downloaded. In the library such a series gets **Tập mới?**, which repeats the search to find new chapters.

### Pages

The menu has three pages: **Tìm truyện** (search every source at once, paste a link, or browse one source), **Thư viện** (the queue of downloads is shown at the top while there is one, then the downloaded series) and **Nguồn** (your websites).

The library keeps each series to one line: the cover, the name, **Chuyển cho Kindle**, a **Thêm ▾** menu (covers, advert cleaning, AZW3, copying to a reader, backups, rename, delete) and the list of volumes (folded away above three), each with **Xem trang**, **Tải về** and a **⋯** menu. Library-wide actions are under **Công cụ ▾**. The **Nguồn** page lists your websites (edit or delete), adds a new one from its home page, and keeps the manual settings, the selector help and the JSON paste under *Nâng cao*.

### One profile for downloads and the library

The reader settings (device, how the page fills the screen, wide pages, auto-crop, e-ink enhancement, advert cutting, reading direction) are one setting shared by the download of a single source, the download from several sources and **Chuyển cho Kindle** in the library. So a series is the same whether it is downloaded ready-made (set the device before downloading) or converted afterwards from the library.

In **Tìm truyện** a link of a series can be pasted for a source the search did not find (or that has no search box); the pasted links join every series you open the chapters of. Under the chapter table you can also divide chapters into volumes by hand (`1:1-8, 2:9-17, 3:18-26`), which wins over MangaDex and over the chapters-per-volume count.
