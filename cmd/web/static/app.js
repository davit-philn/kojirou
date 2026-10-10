"use strict";

// ---------- helpers ----------

const app = document.getElementById("app");
const LANGS = [
  ["vi", "Tiếng Việt"], ["en", "English"], ["ja", "日本語"], ["ko", "한국어"],
  ["zh", "中文 (giản thể)"], ["zh-hk", "中文 (phồn thể)"], ["es", "Español"],
  ["es-la", "Español (LatAm)"], ["pt-br", "Português (BR)"], ["fr", "Français"],
  ["de", "Deutsch"], ["id", "Indonesia"], ["th", "ไทย"], ["ru", "Русский"],
];

function h(tag, props, ...children) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class") el.className = value;
    else if (key.startsWith("on")) el.addEventListener(key.slice(2), value);
    else if (key === "value" || key === "checked" || key === "disabled" || key === "hidden") el[key] = value;
    else el.setAttribute(key, value === true ? "" : value);
  }
  for (const child of children.flat()) {
    if (child === undefined || child === null || child === false) continue;
    el.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return el;
}

async function api(path, options = {}) {
  const init = { method: options.method || "GET", headers: {}, signal: options.signal };
  if (init.method !== "GET") init.headers["X-Kojirou-Client"] = "1";
  if (options.body !== undefined) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(options.body);
  }
  const resp = await fetch(path, init);
  let data = null;
  try { data = await resp.json(); } catch (_) { /* no body */ }
  if (!resp.ok) throw new Error((data && data.error) || `Lỗi ${resp.status}`);
  return data;
}

const store = {
  get(key, fallback) {
    try { const v = localStorage.getItem("kojirou." + key); return v === null ? fallback : JSON.parse(v); }
    catch (_) { return fallback; }
  },
  set(key, value) {
    try { localStorage.setItem("kojirou." + key, JSON.stringify(value)); } catch (_) { /* storage unavailable */ }
  },
};

function toast(message, { bad = false, link } = {}) {
  const el = h("div", { class: "toast" + (bad ? " bad" : "") }, message, link ? [" ", h("a", { href: link.href }, link.text)] : null);
  document.getElementById("toasts").append(el);
  setTimeout(() => el.remove(), bad ? 8000 : 5000);
}

function formatSize(bytes) {
  if (bytes < 1024) return bytes + " B";
  const units = ["KB", "MB", "GB"];
  let v = bytes / 1024, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return v.toFixed(v < 10 ? 1 : 0) + " " + units[i];
}

function formatDate(value) {
  const d = new Date(value);
  return isNaN(d) || d.getFullYear() < 1990 ? "" : d.toLocaleDateString("vi-VN");
}

const spinner = () => h("div", { class: "spinner", role: "progressbar", "aria-label": "Đang tải" });
const errorBox = (err) => h("div", { class: "error-box", role: "alert" }, String(err.message || err));

function langSelect(value, onchange, extra = []) {
  const select = h("select", { "aria-label": "Ngôn ngữ", onchange: (e) => onchange(e.target.value) },
    ...LANGS.map(([code, name]) => h("option", { value: code, selected: code === value }, name)));
  for (const code of extra) {
    if (!LANGS.some(([c]) => c === code)) select.append(h("option", { value: code, selected: code === value }, code));
  }
  select.value = value;
  return select;
}

// ---------- router ----------

let cleanup = null;
const routes = {
  "": browsePage,
  "series": seriesPage,
  "downloads": downloadsPage,
  "library": libraryPage,
  "sources": sourcesPage,
};

function parseHash() {
  const raw = location.hash.replace(/^#\/?/, "");
  const [path, query = ""] = raw.split("?");
  return { path, params: new URLSearchParams(query) };
}

async function render() {
  if (cleanup) { cleanup(); cleanup = null; }
  const { path, params } = parseHash();
  const page = routes[path] || browsePage;
  const navKey = path === "series" || path === "" ? "browse" : path;
  for (const a of document.querySelectorAll("nav a")) a.classList.toggle("active", a.dataset.nav === navKey);
  app.replaceChildren();
  window.scrollTo(0, 0);
  pollBadge();
  try {
    const result = await page(params);
    if (typeof result === "function") cleanup = result;
  } catch (err) {
    app.replaceChildren(errorBox(err));
  }
}
window.addEventListener("hashchange", render);

// ---------- browse ----------

async function browsePage() {
  const sources = await api("/api/config").then((c) => c.sources).catch(() => ["mangadex"]);
  const state = Object.assign({ source: "mangadex", lang: "vi", order: "", adult: false }, store.get("browse", {}));
  if (!sources.includes(state.source)) state.source = "mangadex";
  let query = "";
  let controller = null;

  const results = h("div");
  const body = h("div");
  const tabs = h("div", { class: "tabs", role: "tablist" },
    sources.map((name) => h("button", {
      class: "tab" + (name === state.source ? " active" : ""), role: "tab",
      "aria-selected": String(name === state.source),
      onclick: () => { state.source = name; store.set("browse", state); renderBody(); renderTabs(); },
    }, name === "mangadex" ? "MangaDex" : name)));
  function renderTabs() {
    tabs.querySelectorAll(".tab").forEach((t, i) => {
      const active = sources[i] === state.source;
      t.classList.toggle("active", active);
      t.setAttribute("aria-selected", String(active));
    });
  }
  app.append(h("h1", null, "Duyệt truyện"), tabs, body);
  renderBody();

  function renderBody() {
    if (controller) controller.abort();
    body.replaceChildren();
    if (state.source === "mangadex") mangadexBody(); else selectorBody();
  }

  function selectorBody() {
    const input = h("input", { type: "url", required: true, placeholder: "https://…/ten-truyen", "aria-label": "Địa chỉ trang truyện" });
    body.append(
      h("p", { class: "muted" }, `Nguồn “${state.source}”: dán địa chỉ trang liệt kê chương của một truyện rồi bấm Mở.`),
      h("form", { class: "row", onsubmit: (e) => {
        e.preventDefault();
        const p = new URLSearchParams({ source: state.source, id: input.value.trim(), lang: state.lang });
        location.hash = "#/series?" + p;
      } },
        h("label", { class: "field grow" }, h("span", null, "Địa chỉ trang truyện"), input),
        h("label", { class: "field" }, h("span", null, "Ngôn ngữ"), langSelect(state.lang, (v) => { state.lang = v; store.set("browse", state); })),
        h("button", { class: "btn primary", type: "submit" }, "Mở")));
  }

  function mangadexBody() {
    const q = h("input", { type: "search", placeholder: "Tên truyện (bỏ trống để xem phổ biến)", value: query, "aria-label": "Tìm truyện" });
    const order = h("select", { "aria-label": "Sắp xếp", onchange: (e) => { state.order = e.target.value; store.set("browse", state); } },
      h("option", { value: "" }, "Mặc định"),
      h("option", { value: "followedCount" }, "Phổ biến nhất"),
      h("option", { value: "latestUploadedChapter" }, "Mới cập nhật"),
      h("option", { value: "createdAt" }, "Mới thêm"));
    order.value = state.order;
    const adult = h("input", { type: "checkbox", checked: state.adult, onchange: (e) => { state.adult = e.target.checked; store.set("browse", state); } });
    body.append(
      h("form", { class: "row", onsubmit: (e) => { e.preventDefault(); query = q.value.trim(); load(0); } },
        h("label", { class: "field grow" }, h("span", null, "Tìm kiếm"), q),
        h("label", { class: "field" }, h("span", null, "Có chương dịch"), langSelect(state.lang, (v) => { state.lang = v; store.set("browse", state); })),
        h("label", { class: "field" }, h("span", null, "Sắp xếp"), order),
        h("label", { class: "check" }, adult, "Hiện 18+"),
        h("button", { class: "btn primary", type: "submit" }, "Tìm")),
      results);
    load(0);
  }

  async function load(offset) {
    if (controller) controller.abort();
    controller = new AbortController();
    const loadingEl = spinner();
    if (offset === 0) results.replaceChildren(loadingEl); else results.append(loadingEl);
    const p = new URLSearchParams({ q: query, lang: state.lang, order: state.order, adult: state.adult ? "1" : "0", offset: String(offset) });
    try {
      const res = await api("/api/search?" + p, { signal: controller.signal });
      loadingEl.remove();
      let grid = results.querySelector(".grid");
      if (!grid || offset === 0) { results.replaceChildren(); grid = h("div", { class: "grid" }); results.append(grid); }
      if (res.items.length === 0 && offset === 0) {
        results.replaceChildren(h("div", { class: "empty" }, "Không tìm thấy truyện nào. Thử đổi ngôn ngữ hoặc từ khoá."));
        return;
      }
      for (const item of res.items) grid.append(card(item));
      results.querySelector(".more")?.remove();
      if (offset + res.items.length < res.total) {
        results.append(h("p", { class: "more", style: "text-align:center" },
          h("button", { class: "btn", onclick: () => load(offset + res.items.length) }, `Tải thêm (${res.total - offset - res.items.length} truyện nữa)`)));
      }
    } catch (err) {
      if (err.name === "AbortError") return;
      loadingEl.remove();
      results.append(errorBox(err));
    }
  }

  function card(item) {
    const p = new URLSearchParams({ source: "mangadex", id: item.ID, lang: state.lang });
    return h("a", { class: "card", href: "#/series?" + p },
      item.CoverURL
        ? h("img", { class: "cover", src: item.CoverURL, alt: "", loading: "lazy", referrerpolicy: "no-referrer" })
        : h("div", { class: "cover none" }, "Không có bìa"),
      h("div", { class: "card-body" },
        h("div", { class: "card-title" }, item.Title || "(không tên)"),
        item.Authors && item.Authors.length ? h("div", { class: "muted small" }, item.Authors.slice(0, 2).join(", ")) : null,
        h("div", { class: "chips" },
          item.Year ? h("span", { class: "chip" }, item.Year) : null,
          item.Status ? h("span", { class: "chip" }, item.Status) : null,
          (item.Languages || []).includes(state.lang) ? h("span", { class: "chip hl" }, state.lang) : null)));
  }

  return () => { if (controller) controller.abort(); };
}

// ---------- series ----------

const defaultOptions = {
  format: "cbz", rightToLeft: true, autocrop: false, widepage: "preserve",
  dataSaver: "no", jpegQuality: 90, lossless: false, force: false,
};

async function seriesPage(params) {
  const source = params.get("source") || "mangadex";
  const id = params.get("id") || "";
  let lang = params.get("lang") || store.get("browse", {}).lang || "en";
  const options = Object.assign({}, defaultOptions, store.get("options", {}));
  const selected = new Set();
  let series = null;

  const head = h("div");
  const chaptersBox = h("div");
  const bar = h("div", { class: "actionbar", hidden: true });
  app.append(head, chaptersBox, bar);
  head.append(spinner());
  await loadSeries();

  async function loadSeries() {
    chaptersBox.replaceChildren(spinner());
    selected.clear();
    updateBar();
    const p = new URLSearchParams({ source, id, lang });
    try {
      series = await api("/api/series?" + p);
    } catch (err) {
      head.replaceChildren(h("p", null, h("a", { href: "#/" }, "← Quay lại")));
      chaptersBox.replaceChildren(errorBox(err));
      return;
    }
    renderHead();
    renderChapters();
  }

  function renderHead() {
    const desc = h("div", { class: "desc clamp" }, series.description || "");
    const more = h("button", { class: "btn small", type: "button", onclick: () => {
      const clamped = desc.classList.toggle("clamp");
      more.textContent = clamped ? "Xem thêm" : "Thu gọn";
    } }, "Xem thêm");
    head.replaceChildren(
      h("p", null, h("a", { href: "#/" }, "← Quay lại")),
      h("div", { class: "series-head" },
        series.coverUrl
          ? h("img", { class: "cover", src: series.coverUrl.replace(".256.jpg", ".512.jpg"), alt: "", referrerpolicy: "no-referrer" })
          : h("div", { class: "cover none" }, "Không có bìa"),
        h("div", null,
          h("h1", null, series.title),
          series.authors.length ? h("p", { class: "muted" }, series.authors.join(", ")) : null,
          h("div", { class: "chips" },
            series.year ? h("span", { class: "chip" }, series.year) : null,
            series.status ? h("span", { class: "chip" }, series.status) : null,
            h("span", { class: "chip" }, source === "mangadex" ? "MangaDex" : source)),
          series.description ? [desc, more] : null,
          h("div", { class: "row", style: "margin-top:.75rem" },
            h("label", { class: "field" }, h("span", null, "Ngôn ngữ chương"),
              langSelect(lang, (v) => { lang = v; loadSeries(); }, series.languages))))),
      optionsPanel());
  }

  function optionsPanel() {
    const save = () => store.set("options", options);
    const sel = (key, items, label, hidden) => h("label", { class: "field", hidden },
      h("span", null, label),
      (() => {
        const s = h("select", { onchange: (e) => { options[key] = e.target.value; save(); } },
          ...items.map(([v, t]) => h("option", { value: v }, t)));
        s.value = options[key];
        return s;
      })());
    const chk = (key, label, hidden) => h("label", { class: "check", hidden },
      h("input", { type: "checkbox", checked: options[key], onchange: (e) => { options[key] = e.target.checked; save(); } }), label);
    const quality = h("input", { type: "number", min: "1", max: "100", value: String(options.jpegQuality),
      onchange: (e) => { options.jpegQuality = Math.min(100, Math.max(1, parseInt(e.target.value, 10) || 90)); e.target.value = options.jpegQuality; save(); } });
    return h("details", { class: "panel" },
      h("summary", null, "Tuỳ chọn tải xuống"),
      h("div", { class: "options" },
        sel("format", [["cbz", "CBZ (đa số app đọc truyện)"], ["mobi", "MOBI/AZW3 (Kindle)"]], "Định dạng"),
        sel("widepage", [["preserve", "Giữ nguyên"], ["split", "Tách đôi"], ["preserve-and-split", "Giữ + tách đôi"], ["split-and-preserve", "Tách đôi + giữ"]], "Trang ngang"),
        source === "mangadex" ? sel("dataSaver", [["no", "Ảnh gốc"], ["prefer", "Ảnh nhẹ"], ["fallback", "Ảnh gốc, lỗi thì dùng ảnh nhẹ"]], "Chất lượng ảnh") : null,
        h("label", { class: "field" }, h("span", null, "Chất lượng JPEG (CBZ)"), quality),
        h("div", { class: "field" }, h("span", null, "Khác"),
          chk("rightToLeft", "Đọc từ phải sang trái"),
          chk("autocrop", "Tự cắt viền trắng"),
          chk("lossless", "CBZ lưu PNG (không nén mất dữ liệu)"),
          chk("force", "Tải lại cả tập đã có"))));
  }

  function renderChapters() {
    if (series.volumes.length === 0) {
      chaptersBox.replaceChildren(h("div", { class: "empty" }, `Không có chương nào bằng ngôn ngữ “${lang}”. Thử đổi ngôn ngữ chương.`));
      return;
    }
    const all = series.volumes.flatMap((v) => v.chapters);
    const checks = new Map(); // chapter id -> checkbox
    const volChecks = [];
    const syncVolumes = () => {
      for (const { box, chapters } of volChecks) {
        const n = chapters.filter((c) => selected.has(c.id)).length;
        box.checked = n === chapters.length && n > 0;
        box.indeterminate = n > 0 && n < chapters.length;
      }
    };
    const setChapter = (c, on) => { on ? selected.add(c.id) : selected.delete(c.id); checks.get(c.id).checked = on; };
    const refresh = () => { syncVolumes(); updateBar(); };

    const from = h("input", { type: "text", placeholder: "từ", size: "5", "aria-label": "Từ chương" });
    const to = h("input", { type: "text", placeholder: "đến", size: "5", "aria-label": "Đến chương" });
    const toolbar = h("div", { class: "row", style: "margin-top:1rem" },
      h("button", { class: "btn small", onclick: () => { all.forEach((c) => setChapter(c, true)); refresh(); } }, "Chọn tất cả"),
      h("button", { class: "btn small", onclick: () => { all.forEach((c) => setChapter(c, false)); refresh(); } }, "Bỏ chọn"),
      h("span", { class: "muted small" }, `${all.length} chương, ${series.volumes.length} tập`),
      h("span", { class: "range" },
        h("span", { class: "small muted" }, "Chọn khoảng:"), from, "–", to,
        h("button", { class: "btn small", onclick: () => {
          const a = parseFloat(from.value), b = parseFloat(to.value);
          if (isNaN(a) || isNaN(b)) { toast("Nhập số chương bắt đầu và kết thúc", { bad: true }); return; }
          const [lo, hi] = a <= b ? [a, b] : [b, a];
          for (const c of all) { const n = parseFloat(c.number); if (!isNaN(n) && n >= lo && n <= hi) setChapter(c, true); }
          refresh();
        } }, "Chọn")));

    const list = series.volumes.map((vol) => {
      const volBox = h("input", { type: "checkbox", "aria-label": `Chọn cả tập ${vol.id}`, onclick: (e) => e.stopPropagation(),
        onchange: (e) => { vol.chapters.forEach((c) => setChapter(c, e.target.checked)); refresh(); } });
      volChecks.push({ box: volBox, chapters: vol.chapters });
      return h("details", { class: "volume", open: true },
        h("summary", null, volBox, `Tập ${vol.id}`, h("span", { class: "muted small" }, ` · ${vol.chapters.length} chương`)),
        vol.chapters.map((c) => {
          const box = h("input", { type: "checkbox", onchange: (e) => { setChapter(c, e.target.checked); refresh(); } });
          checks.set(c.id, box);
          return h("label", { class: "chapter" }, box,
            h("span", { class: "name" }, `Chương ${c.number}`, c.title && c.title !== c.number ? ` — ${c.title}` : ""),
            h("span", { class: "meta" }, [c.groups.join(", "), formatDate(c.published)].filter(Boolean).join(" · ")));
        }));
    });
    chaptersBox.replaceChildren(toolbar, ...list);
  }

  function updateBar() {
    bar.hidden = selected.size === 0;
    if (selected.size === 0) { bar.replaceChildren(); return; }
    const button = h("button", { class: "btn primary", onclick: startDownload }, `Tải xuống (${options.format.toUpperCase()})`);
    bar.replaceChildren(h("strong", null, `Đã chọn ${selected.size} chương`), button);
  }

  async function startDownload(e) {
    const button = e.currentTarget;
    button.disabled = true;
    try {
      await api("/api/downloads", { method: "POST", body: {
        source, id, title: series.title, lang, chapterIds: [...selected],
        format: options.format, rightToLeft: options.rightToLeft, autocrop: options.autocrop,
        widepage: options.widepage, dataSaver: source === "mangadex" ? options.dataSaver : "no",
        jpegQuality: options.jpegQuality, lossless: options.lossless, force: options.force,
      } });
      toast(`Đã thêm “${series.title}” vào hàng đợi.`, { link: { href: "#/downloads", text: "Xem tiến độ" } });
      pollBadge();
    } catch (err) {
      toast(err.message, { bad: true });
    } finally {
      button.disabled = false;
    }
  }
}

// ---------- downloads ----------

const STATE_LABEL = { queued: "Chờ", running: "Đang tải", done: "Xong", failed: "Lỗi", canceled: "Đã huỷ" };
const STATE_CLASS = { done: "ok", failed: "bad", running: "hl" };

async function downloadsPage() {
  const list = h("div");
  app.append(h("h1", null, "Đang tải"), list);
  let stopped = false, timer = null, busy = false;

  async function refresh() {
    if (busy || stopped) return;
    busy = true;
    try {
      const jobs = await api("/api/downloads");
      if (stopped) return;
      list.replaceChildren(...(jobs.length
        ? jobs.map(jobCard)
        : [h("div", { class: "empty" }, "Chưa có lượt tải nào. Vào ", h("a", { href: "#/" }, "Duyệt truyện"), " để chọn truyện.")]));
    } catch (err) {
      if (!stopped) list.replaceChildren(errorBox(err));
    } finally { busy = false; }
  }
  await refresh();
  timer = setInterval(refresh, 1000);
  return () => { stopped = true; clearInterval(timer); };
}

function jobCard(job) {
  const active = job.state === "queued" || job.state === "running";
  const steps = (job.steps || []).filter((s) => !s.vanishing || s.status === "running");
  const visible = steps.slice(-6);
  return h("div", { class: "job" },
    h("div", { class: "job-head" },
      h("span", { class: "title" }, job.title || "(đang lấy thông tin)"),
      h("span", { class: "chip" }, job.source),
      h("span", { class: "chip" }, job.format.toUpperCase()),
      job.chapters ? h("span", { class: "chip" }, `${job.chapters} chương`) : null,
      h("span", { class: "chip " + (STATE_CLASS[job.state] || "") }, STATE_LABEL[job.state] || job.state),
      active ? h("button", { class: "btn small danger", onclick: async () => {
        try { await api("/api/downloads/" + encodeURIComponent(job.id), { method: "DELETE" }); } catch (e) { toast(e.message, { bad: true }); }
      } }, "Huỷ") : null,
      job.state === "done" ? h("a", { class: "btn small", href: "#/library" }, "Mở thư viện") : null),
    job.error ? errorBox(job.error) : null,
    visible.length ? h("div", { class: "steps" }, visible.map(stepRow)) : null);
}

function stepRow(s) {
  const known = s.total > 0;
  const pct = known ? Math.min(100, Math.round((s.current / s.total) * 100)) : 0;
  const finished = s.status === "done" || s.status === "skipped";
  const bar = h("div", { class: "bar" + (finished ? " done" : "") + (!known && !finished ? " indeterminate" : "") },
    h("i", { style: `width:${finished ? 100 : pct}%` }));
  const label = s.status === "skipped" ? "Đã có, bỏ qua"
    : s.status === "error" ? (s.message || "Lỗi")
    : finished ? "Xong" : known ? `${s.current} / ${s.total}` : "…";
  return h("div", { class: "step" }, h("span", null, stepTitle(s.title)), bar, h("span", { class: "muted" }, label));
}

// The pipeline reports English step names; show them in Vietnamese.
function stepTitle(title) {
  const volume = /^Volume: (.+)$/.exec(title);
  if (volume) return `Tập ${volume[1]}`;
  return { "Covers": "Ảnh bìa", "Writing...": "Ghi file", "Disk...": "Đọc từ ổ đĩa" }[title] || title;
}

// badge in the navigation bar
let badgeTimer = null;
async function pollBadge() {
  const badge = document.getElementById("badge");
  try {
    const jobs = await api("/api/downloads");
    const n = jobs.filter((j) => j.state === "queued" || j.state === "running").length;
    badge.textContent = String(n);
    badge.hidden = n === 0;
  } catch (_) { /* server may be restarting */ }
}
badgeTimer = setInterval(() => { if (!document.hidden) pollBadge(); }, 3000);

// ---------- library ----------

async function libraryPage() {
  const [lib, cfg] = await Promise.all([api("/api/library"), api("/api/config")]);
  const refresh = h("button", { class: "btn small", onclick: render }, "Làm mới");
  app.append(h("h1", null, "Thư viện"),
    h("p", { class: "muted" }, "Thư mục lưu trên máy: ", h("code", null, cfg.library), " ", refresh));
  if (lib.length === 0) {
    app.append(h("div", { class: "empty" }, "Thư viện đang trống. Các tập đã tải sẽ hiện ở đây."));
    return;
  }
  for (const series of lib) {
    app.append(h("section", { class: "series-block" },
      h("h2", null, series.name, " ", h("span", { class: "muted small" }, `(${series.files.length} tập)`)),
      series.files.map((f) => h("div", { class: "file" },
        h("span", { class: "name" }, f.name),
        h("span", { class: "chip" }, f.format.toUpperCase()),
        h("span", { class: "muted small" }, formatSize(f.size)),
        h("span", { class: "muted small" }, formatDate(f.modified)),
        h("a", { class: "btn small primary", download: "",
          href: `/api/library/${encodeURIComponent(series.name)}/${encodeURIComponent(f.name)}` }, "Tải về")))));
  }
}

// ---------- sources ----------

const SOURCE_FIELDS = [
  ["base_url", "Địa chỉ gốc (base_url)", "text", "https://ten-mien.com"],
  ["chapter_list_selector", "Selector danh sách chương", "text", ".list-chapter a"],
  ["image_list_selector", "Selector ảnh trang truyện", "text", ".reading-content img"],
  ["image_attr", "Thuộc tính chứa URL ảnh", "text", "src hoặc data-original"],
  ["user_agent", "User-Agent (tuỳ chọn)", "text", ""],
  ["referer", "Referer (tuỳ chọn)", "text", ""],
  ["title", "Tên truyện (tuỳ chọn)", "text", ""],
  ["chapters_per_volume", "Số chương mỗi tập (0 = một tập)", "number", "0"],
  ["max_concurrent_downloads", "Tải song song tối đa (1-8)", "number", "4"],
  ["timeout_seconds", "Timeout mỗi request (giây)", "number", "30"],
];

async function sourcesPage() {
  const saved = await api("/api/sources");
  const inputs = {};
  const resultBox = h("div");
  const name = h("input", { type: "text", placeholder: "vd: trangA", "aria-label": "Tên nguồn" });
  const testURL = h("input", { type: "url", placeholder: "https://…/trang-liet-ke-chuong", "aria-label": "Địa chỉ trang truyện để thử" });
  const pasted = h("textarea", { placeholder: '{"base_url": "...", "chapter_list_selector": "...", ...}', "aria-label": "JSON cấu hình" });

  const fields = SOURCE_FIELDS.map(([key, label, type, placeholder]) => {
    inputs[key] = h("input", { type, placeholder, "aria-label": label });
    return h("label", { class: "field" }, h("span", null, label), inputs[key]);
  });

  function readConfig() {
    const cfg = {};
    for (const [key, , type] of SOURCE_FIELDS) {
      const v = inputs[key].value.trim();
      if (v === "") continue;
      cfg[key] = type === "number" ? Number(v) : v;
    }
    return cfg;
  }
  function fillConfig(cfg) {
    for (const [key] of SOURCE_FIELDS) inputs[key].value = cfg[key] === undefined || cfg[key] === 0 ? "" : String(cfg[key]);
  }

  const list = h("div", { class: "src-list" }, saved.length
    ? saved.map((s) => h("div", { class: "src" },
        h("span", { class: "name" }, s.name),
        h("span", { class: "url muted small" }, s.config.base_url || "(không có base_url)"),
        h("button", { class: "btn small", onclick: () => { name.value = s.name; fillConfig(s.config); window.scrollTo({ top: document.body.scrollHeight, behavior: "smooth" }); } }, "Sửa"),
        h("button", { class: "btn small", onclick: () => { const st = Object.assign({ lang: "vi", order: "", adult: false }, store.get("browse", {})); st.source = s.name; store.set("browse", st); location.hash = "#/"; } }, "Dùng"),
        h("button", { class: "btn small danger", onclick: async () => {
          if (!confirm(`Xoá nguồn “${s.name}”?`)) return;
          try { await api("/api/sources/" + encodeURIComponent(s.name), { method: "DELETE" }); render(); } catch (e) { toast(e.message, { bad: true }); }
        } }, "Xoá")))
    : [h("p", { class: "muted" }, "Chưa có nguồn nào. Thêm một nguồn bên dưới.")]);

  app.append(
    h("h1", null, "Nguồn"),
    h("p", { class: "muted" }, "Ngoài MangaDex, bạn có thể thêm trang web bằng CSS selector. Chỉ dùng với trang mà điều khoản cho phép tải tự động và nội dung bạn có quyền đọc."),
    list,
    h("h2", { style: "margin-top:2rem" }, "Thêm hoặc sửa nguồn"),
    h("details", { class: "panel" },
      h("summary", null, "Cách tìm selector"),
      h("ol", null,
        h("li", null, "Mở trang liệt kê chương, bấm F12, dùng công cụ chọn phần tử rồi bấm vào tên một chương."),
        h("li", null, "Ghi lại thẻ cha có class đặc trưng, ví dụ ", h("code", null, ".list-chapter a"), "."),
        h("li", null, "Mở một chương, chọn một ảnh và xem URL nằm ở thuộc tính nào (", h("code", null, "src"), ", ", h("code", null, "data-original"), "…)."),
        h("li", null, "Điền vào form rồi bấm Thử. Chỉ đọc được HTML gốc, trang nạp ảnh bằng JavaScript sẽ không dùng được."))),
    h("div", { class: "panel" },
      h("label", { class: "field" }, h("span", null, "Tên nguồn"), name),
      h("div", { class: "options" }, fields),
      h("details", { style: "margin-top:.75rem" }, h("summary", null, "Dán JSON cấu hình"),
        pasted,
        h("button", { class: "btn small", style: "margin-top:.5rem", onclick: () => {
          try { fillConfig(JSON.parse(pasted.value)); } catch (e) { toast("JSON không hợp lệ: " + e.message, { bad: true }); }
        } }, "Điền vào form")),
      h("div", { class: "row", style: "margin-top:1rem" },
        h("label", { class: "field grow" }, h("span", null, "Thử với trang truyện"), testURL),
        h("button", { class: "btn", onclick: runTest }, "Thử"),
        h("button", { class: "btn primary", onclick: save }, "Lưu nguồn")),
      resultBox));

  async function runTest(e) {
    const button = e.currentTarget;
    button.disabled = true;
    resultBox.replaceChildren(spinner());
    try {
      const res = await api("/api/sources/test", { method: "POST", body: { config: readConfig(), url: testURL.value.trim() } });
      const lines = [`Tìm thấy ${res.chapterCount} chương. Ví dụ:`, ...res.chapters.map((c) => `  • ${c.Title}  →  ${c.URL}`)];
      if (res.imageError) lines.push("", "Không lấy được ảnh của chương đầu: " + res.imageError);
      else lines.push("", `Chương đầu có ${res.imageCount} ảnh. Ví dụ:`, ...res.images.map((u) => "  • " + u));
      resultBox.replaceChildren(h("pre", { class: "result" }, lines.join("\n")));
    } catch (err) {
      resultBox.replaceChildren(errorBox(err));
    } finally { button.disabled = false; }
  }

  async function save() {
    try {
      await api("/api/sources", { method: "POST", body: { name: name.value.trim(), config: readConfig() } });
      toast("Đã lưu nguồn.");
      render();
    } catch (err) {
      toast(err.message, { bad: true });
    }
  }
}

// ---------- start ----------

pollBadge();
render();
