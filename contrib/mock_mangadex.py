#!/usr/bin/env python3
"""Minimal fake of the MangaDex endpoints used by kojirou (stdlib only).

Try the web interface without internet access or a real series:

    python3 contrib/mock_mangadex.py 9100
    kojirou serve --api-url http://127.0.0.1:9100/ --covers-url http://127.0.0.1:9100/covers/
"""
import http.server, json, re, struct, sys, urllib.parse, zlib

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 9100
BASE = f"http://127.0.0.1:{PORT}"

def uid(n): return f"{n:08d}-0000-4000-8000-{n:012d}"

def png(w, h, shade):
    raw = b"".join(b"\x00" + bytes([(shade + x * 3) % 256 for x in range(w)]) for _ in range(h))
    def ch(t, d):
        body = t + d
        return struct.pack(">I", len(d)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)
    return (b"\x89PNG\r\n\x1a\n" + ch(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 0, 0, 0, 0))
            + ch(b"IDAT", zlib.compress(raw)) + ch(b"IEND", b""))

TITLES = ["Demo Dragon", "Another Story", "Moonlight Garden", "Iron Harbor", "Paper Lanterns",
          "The Quiet Orchard", "Starfall Academy", "Salt and Ember"]
SERIES = {}
for i, title in enumerate(TITLES, start=1):
    langs = ["en", "vi"] if i % 2 else ["en"]
    chapters = []
    n_ch = 6 if i == 1 else 2
    for c in range(1, n_ch + 1):
        for lang in langs:
            chapters.append({"id": uid(1000 * i + c * 10 + (1 if lang == "vi" else 0)), "chapter": str(c),
                             "volume": str(1 if c <= 3 else 2), "title": f"Episode {c}", "lang": lang,
                             "group": uid(9000 + (1 if lang == "vi" else 2)), "pages": 3})
    SERIES[uid(i)] = {"title": title, "langs": langs, "chapters": chapters, "year": 2018 + i, "status": "ongoing"}
GROUPS = {uid(9001): "Nhom Dich Viet", uid(9002): "English Scans"}
CHAPTERS = {c["id"]: (sid, c) for sid, s in SERIES.items() for c in s["chapters"]}

def item(sid, s):
    return {"id": sid, "type": "manga", "attributes": {
        "title": {"en": s["title"]}, "description": {"en": f"{s['title']} is a made-up series used for testing.\nSecond line."},
        "status": s["status"], "year": s["year"], "availableTranslatedLanguages": s["langs"], "contentRating": "safe"},
        "relationships": [
            {"id": uid(500), "type": "author", "attributes": {"name": "Test Author"}},
            {"id": uid(501), "type": "cover_art", "attributes": {"fileName": "cover.jpg"}}]}

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *a): sys.stderr.write("mock: " + (fmt % a) + "\n")
    def send(self, body, kind="application/json", code=200):
        if not isinstance(body, bytes): body = json.dumps(body).encode()
        self.send_response(code); self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def do_GET(self):
        u = urllib.parse.urlparse(self.path); q = urllib.parse.parse_qs(u.query)
        path = re.sub(r"/+", "/", u.path)
        if path.startswith("/covers/"): return self.send(png(200, 300, 90), "image/png")
        if path.startswith("/imgs/"):
            seed = sum(map(ord, path)) % 200
            return self.send(png(120, 180, seed), "image/png")
        if path == "/manga":
            title = (q.get("title") or [""])[0].lower(); lang = (q.get("availableTranslatedLanguage[]") or [""])[0]
            found = [(k, s) for k, s in SERIES.items() if title in s["title"].lower() and (not lang or lang in s["langs"])]
            off = int((q.get("offset") or ["0"])[0]); lim = int((q.get("limit") or ["24"])[0])
            return self.send({"result": "ok", "limit": lim, "offset": off, "total": len(found),
                              "data": [item(k, s) for k, s in found[off:off + lim]]})
        m = re.fullmatch(r"/manga/([^/]+)", path)
        if m and m.group(1) in SERIES:
            sid = m.group(1); s = SERIES[sid]
            if "includes[]" in q: return self.send({"result": "ok", "data": item(sid, s)})
            d = item(sid, s); d["relationships"] = [{"id": uid(500), "type": "author"}, {"id": uid(500), "type": "artist"}]
            return self.send({"result": "ok", "response": "entity", "data": d})
        if path == "/author":
            ids = q.get("ids[]", [])
            return self.send({"result": "ok", "total": len(ids), "data": [{"id": i, "attributes": {"name": "Test Author"}} for i in ids]})
        m = re.fullmatch(r"/manga/([^/]+)/feed", path)
        if m and m.group(1) in SERIES:
            cs = SERIES[m.group(1)]["chapters"]
            data = [{"id": c["id"], "type": "chapter", "attributes": {
                        "title": c["title"], "volume": c["volume"], "chapter": c["chapter"], "pages": c["pages"],
                        "translatedLanguage": c["lang"], "publishAt": "2024-05-0%dT10:00:00+00:00" % (int(c["chapter"]) % 9 + 1)},
                     "relationships": [{"id": c["group"], "type": "scanlation_group"}]} for c in cs]
            return self.send({"result": "ok", "total": len(data), "limit": 500, "offset": 0, "data": data})
        if path == "/group":
            ids = q.get("ids[]", [])
            return self.send({"result": "ok", "total": len(ids), "data": [{"id": i, "attributes": {"name": GROUPS.get(i, "?")}} for i in ids]})
        if path == "/cover":
            mid = (q.get("manga[]") or [""])[0]
            data = [{"id": uid(700 + v), "attributes": {"volume": str(v), "fileName": f"v{v}.jpg"}} for v in (1, 2)] if mid in SERIES else []
            return self.send({"result": "ok", "total": len(data), "data": data})
        m = re.fullmatch(r"/at-home/server/([^/]+)", path)
        if m and m.group(1) in CHAPTERS:
            sid, c = CHAPTERS[m.group(1)]
            files = [f"p{i}.png" for i in range(1, c["pages"] + 1)]
            return self.send({"result": "ok", "baseUrl": BASE + "/imgs", "chapter": {"hash": "h" + c["id"][-4:], "data": files, "dataSaver": files}})
        self.send({"errors": [{"detail": "not found: " + path}]}, code=404)

http.server.ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
