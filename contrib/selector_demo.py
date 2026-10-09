#!/usr/bin/env python3
"""Serve a small demo site to try `kojirou --source-config` without any real website.

Usage:
    python3 contrib/selector_demo.py            # serves on http://127.0.0.1:8765
    python3 contrib/selector_demo.py --port 9000

It writes source.json next to this script's working directory and prints the
command to run in a second terminal. Only the Python standard library is used.
"""
import argparse
import http.server
import json
import struct
import zlib

CHAPTERS = [3, 2, 1]  # newest first, like most sites
PAGES_PER_CHAPTER = 4


def png(width, height, shade):
    raw = b"".join(b"\x00" + bytes([shade]) * width for _ in range(height))

    def chunk(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    header = struct.pack(">IIBBBBB", width, height, 8, 0, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", header)
        + chunk(b"IDAT", zlib.compress(raw))
        + chunk(b"IEND", b"")
    )


def series_page():
    links = "".join(f'<a href="/c/{n}">Chapter {n}</a>' for n in CHAPTERS)
    return f'<html><body><div class="chapters">{links}</div></body></html>'.encode()


def chapter_page(n):
    imgs = "".join(
        f'<img data-original="/img/{n}-{i}.png" src="/placeholder.gif">'
        for i in range(1, PAGES_PER_CHAPTER + 1)
    )
    return f'<html><body><div id="reader">{imgs}</div></body></html>'.encode()


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        print("  request:", self.path)

    def reply(self, body, kind):
        self.send_response(200)
        self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        parts = self.path.strip("/").split("/")
        try:
            if parts[0] == "series":
                return self.reply(series_page(), "text/html")
            if parts[0] == "c":
                return self.reply(chapter_page(int(parts[1])), "text/html")
            if parts[0] == "img":
                chapter, page = parts[1].removesuffix(".png").split("-")
                return self.reply(png(60, 90, 40 * int(page) + 10 * int(chapter)), "image/png")
        except (ValueError, IndexError):
            pass
        self.send_error(404)


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--port", type=int, default=8765)
    port = parser.parse_args().port
    base = f"http://127.0.0.1:{port}"

    config = {
        "base_url": base,
        "chapter_list_selector": ".chapters a",
        "image_list_selector": "#reader img",
        "image_attr": "data-original",
        "title": "Demo Series",
        "chapters_per_volume": 2,
        "max_concurrent_downloads": 4,
    }
    with open("source.json", "w") as f:
        json.dump(config, f, indent=2)

    print(f"Demo site on {base} (Ctrl+C to stop). Wrote source.json.\nIn another terminal run:\n")
    print(f"  ./kojirou {base}/series/demo -l en --source-config source.json --format cbz -o demo-out\n")
    http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
