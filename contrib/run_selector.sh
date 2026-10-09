#!/usr/bin/env sh
# Run the steps one after another: build, preview the chapter list, ask, download.
#
# Usage: contrib/run_selector.sh <series-url> [source.json] [mobi|cbz] [out-dir]
# Env:   LANG_CODE (default: en)
set -eu

URL="${1:?usage: $0 <series-url> [source.json] [mobi|cbz] [out-dir]}"
CONFIG="${2:-source.json}"
FORMAT="${3:-cbz}"
OUT="${4:-out}"
LANG_CODE="${LANG_CODE:-en}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "[1/3] Build"
go build -C "$ROOT" -o "$ROOT/kojirou" .

echo "[2/3] Preview chapters (nothing is downloaded)"
"$ROOT/kojirou" "$URL" -l "$LANG_CODE" --source-config "$CONFIG" --dry-run

printf "Continue with the download? [y/N] "
read -r answer
case "$answer" in
  y | Y) ;;
  *) echo "Stopped."; exit 0 ;;
esac

echo "[3/3] Download to $OUT as $FORMAT"
"$ROOT/kojirou" "$URL" -l "$LANG_CODE" --source-config "$CONFIG" --format "$FORMAT" --out "$OUT"
echo "Done."
