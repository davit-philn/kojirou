"use strict";

// ---------- helpers ----------

const app = document.getElementById("app");
const LANGS = [
  ["vi", "Tiếng Việt"], ["en", "English"], ["ja", "日本語"], ["ko", "한국어"],
  ["zh", "中文 (giản thể)"], ["zh-hk", "中文 (phồn thể)"], ["es", "Español"],
  ["es-la", "Español (LatAm)"], ["pt-br", "Português (BR)"], ["fr", "Français"],
  ["de", "Deutsch"], ["id", "Indonesia"], ["th", "ไทย"], ["ru", "Русский"],
];

// nodes drops the empty entries (null, false) that replaceChildren would print as text.
const nodes = (...list) => list.flat().filter((n) => n !== null && n !== undefined && n !== false);

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
  const timeoutMs = options.timeout || 120000;
  const init = { method: options.method || "GET", headers: {}, signal: options.signal || AbortSignal.timeout(timeoutMs) };
  if (init.method !== "GET") init.headers["X-Kojirou-Client"] = "1";
  if (options.body !== undefined) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(options.body);
  }
  let resp;
  try {
    resp = await fetch(path, init);
  } catch (err) {
    if (err.name === "TimeoutError") {
      throw new Error(`Máy chủ không trả lời sau ${Math.round(timeoutMs / 1000)} giây. Xem cửa sổ terminal đang chạy kojirou để biết chi tiết.`);
    }
    if (err.name === "AbortError") throw err;
    throw new Error("Không kết nối được tới kojirou. Cửa sổ terminal đã bị đóng chưa?");
  }
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

// Accepts a MangaDex series ID or a mangadex.org/title/<id>/… link.
function seriesIdFromText(text) {
  const uuid = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";
  const t = text.trim();
  if (new RegExp(`^${uuid}$`, "i").test(t)) return t.toLowerCase();
  const m = new RegExp(`^https?://(?:www\\.)?mangadex\\.org/title/(${uuid})(?:[/?#]|$)`, "i").exec(t);
  return m ? m[1].toLowerCase() : null;
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
  const config = await api("/api/config").catch(() => ({ sources: ["mangadex"], catalogues: {} }));
  const sources = config.sources;
  const catalogues = config.catalogues || {};
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
    if (state.source === "mangadex") mangadexBody();
    else if (catalogues[state.source]) catalogueBody(catalogues[state.source]);
    else selectorBody();
  }

  // A source whose website was scanned for its list of series: browse and search it like MangaDex.
  function catalogueBody(info) {
    const name = state.source;
    const q = h("input", { type: "search", placeholder: info.search ? "Tìm truyện trên nguồn này" : "Nguồn này không có ô tìm kiếm", disabled: !info.search, "aria-label": "Tìm truyện" });
    const other = h("input", { type: "url", placeholder: "https://… một trang danh sách khác của website", "aria-label": "Trang danh sách khác" });
    const grid = h("div", { class: "grid" });
    const status = h("div");
    const more = h("p", { class: "more", style: "text-align:center" });
    const manual = h("input", { type: "url", placeholder: "https://…/ten-truyen", "aria-label": "Địa chỉ trang truyện" });

    body.append(
      h("form", { class: "row", onsubmit: (e) => { e.preventDefault(); load({ text: q.value.trim() }); } },
        h("label", { class: "field grow" }, h("span", null, `Nguồn “${name}”`), q),
        h("button", { class: "btn primary", type: "submit" }, info.search ? "Tìm" : "Tải lại")),
      h("details", { class: "panel" },
        h("summary", null, "Mở trang khác hoặc dán địa chỉ một truyện"),
        h("form", { class: "row", onsubmit: (e) => { e.preventDefault(); if (other.value.trim()) load({ page: other.value.trim() }); } },
          h("label", { class: "field grow" }, h("span", null, "Xem trang danh sách khác của website"), other),
          h("button", { class: "btn", type: "submit" }, "Xem")),
        h("form", { class: "row", style: "margin-top:.5rem", onsubmit: (e) => {
          e.preventDefault();
          if (!manual.value.trim()) return;
          location.hash = "#/series?" + new URLSearchParams({ source: name, id: manual.value.trim(), lang: state.lang });
        } },
          h("label", { class: "field grow" }, h("span", null, "Địa chỉ trang truyện"), manual),
          h("label", { class: "field" }, h("span", null, "Ngôn ngữ"), langSelect(state.lang, (v) => { state.lang = v; store.set("browse", state); })),
          h("button", { class: "btn", type: "submit" }, "Mở"))),
      status, grid, more);
    load({});

    async function load({ page, text, append }) {
      if (controller) controller.abort();
      controller = new AbortController();
      const spin = spinner();
      if (!append) { grid.replaceChildren(); more.replaceChildren(); status.replaceChildren(spin); } else more.replaceChildren(spin);
      const p = new URLSearchParams({ source: name });
      if (text) p.set("q", text); else if (page) p.set("page", page);
      try {
        const res = await api("/api/browse?" + p, { signal: controller.signal, timeout: 60000 });
        status.replaceChildren();
        more.replaceChildren();
        if (res.items.length === 0 && !append) status.replaceChildren(h("div", { class: "empty" }, "Không tìm thấy truyện nào."));
        for (const item of res.items) grid.append(seriesCard(name, item));
        if (res.next) {
          more.append(h("button", { class: "btn", onclick: () => load({ page: res.next, append: true }) }, "Tải thêm"));
        }
      } catch (err) {
        if (err.name === "AbortError") return;
        status.replaceChildren(errorBox(err));
        more.replaceChildren();
      }
    }
  }

  function seriesCard(source, item) {
    const p = new URLSearchParams({ source, id: item.url, lang: state.lang });
    if (item.title) p.set("title", item.title);
    return h("a", { class: "card", href: "#/series?" + p },
      item.coverUrl
        ? h("img", { class: "cover", src: "/api/cover?" + new URLSearchParams({ source, url: item.coverUrl }), alt: "", loading: "lazy" })
        : h("div", { class: "cover none" }, "Không có bìa"),
      h("div", { class: "card-body" }, h("div", { class: "card-title" }, item.title || "(không tên)")));
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
    const q = h("input", { type: "search", placeholder: "Tên truyện hoặc dán link MangaDex (bỏ trống để xem phổ biến)", value: query, "aria-label": "Tìm truyện" });
    const order = h("select", { "aria-label": "Sắp xếp", onchange: (e) => { state.order = e.target.value; store.set("browse", state); } },
      h("option", { value: "" }, "Mặc định"),
      h("option", { value: "followedCount" }, "Phổ biến nhất"),
      h("option", { value: "latestUploadedChapter" }, "Mới cập nhật"),
      h("option", { value: "createdAt" }, "Mới thêm"));
    order.value = state.order;
    const adult = h("input", { type: "checkbox", checked: state.adult, onchange: (e) => { state.adult = e.target.checked; store.set("browse", state); } });
    body.append(
      h("form", { class: "row", onsubmit: (e) => {
        e.preventDefault();
        query = q.value.trim();
        const id = seriesIdFromText(query);
        if (id) { location.hash = "#/series?" + new URLSearchParams({ source: "mangadex", id, lang: state.lang }); return; }
        load(0);
      } },
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
  // A second website that fills the chapters MangaDex lacks (MangaDex series only).
  let fill = { source: "", url: "" };
  let linked = false;
  let savedSources = [];
  // MangaDex series that decides the volumes of a series from your own source.
  let volumesFrom = source === "mangadex" ? "" : (store.get("volumesFrom", {})[id] || "");
  let siteTitle = "";
  let candidates = null; // search results shown in the panel; null = not searched yet
  let searching = false;
  let searchSeq = 0; // a newer search makes an older answer obsolete
  let volPanel = null;
  let manualOpen = false;
  // Chapters moved to another volume by hand: chapter id -> volume ("3", "3.5" or "Special").
  const overrideKey = source + "|" + id;
  let overrides = store.get("volumeOverrides", {})[overrideKey] || {};

  const fillActive = () => source === "mangadex" && fill.source !== "" && fill.url !== "";

  const head = h("div");
  const chaptersBox = h("div");
  const bar = h("div", { class: "actionbar", hidden: true });
  app.append(head, chaptersBox, bar);
  head.append(spinner());
  if (source === "mangadex") {
    try {
      const [sources, links] = await Promise.all([api("/api/sources"), api("/api/links")]);
      savedSources = sources;
      const link = links[id.toLowerCase()];
      if (link) { fill = { source: link.source, url: link.url }; linked = true; }
    } catch (_) { /* the fill option is optional */ }
  }
  await loadSeries();

  async function loadSeries() {
    chaptersBox.replaceChildren(spinner(), h("p", { class: "muted small", style: "text-align:center" },
      "Đang lấy danh sách chương… truyện có nhiều chương hoặc nhiều nhóm dịch có thể mất vài chục giây."));
    selected.clear();
    updateBar();
    const p = new URLSearchParams({ source, id, lang });
    if (source !== "mangadex" && params.get("title")) p.set("title", params.get("title"));
    if (fillActive()) { p.set("fillSource", fill.source); p.set("fillUrl", fill.url); }
    if (volumesFrom) p.set("volumesFrom", volumesFrom);
    try {
      series = await api("/api/series?" + p);
    } catch (err) {
      head.replaceChildren(...nodes(h("p", null, h("a", { href: "#/" }, "← Quay lại")), fillPanel(), volumesPanel()));
      chaptersBox.replaceChildren(errorBox(err));
      return;
    }
    if (!siteTitle && !series.volumesFrom) siteTitle = series.title;
    renderHead();
    renderChapters();
  }

  function renderHead() {
    const desc = h("div", { class: "desc clamp" }, series.description || "");
    const more = h("button", { class: "btn small", type: "button", onclick: () => {
      const clamped = desc.classList.toggle("clamp");
      more.textContent = clamped ? "Xem thêm" : "Thu gọn";
    } }, "Xem thêm");
    head.replaceChildren(...nodes(
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
      fillPanel(),
      volumesPanel(),
      optionsPanel()));
  }

  function setVolumesFrom(dexId) {
    volumesFrom = dexId;
    const saved = store.get("volumesFrom", {});
    if (dexId) saved[id] = dexId; else delete saved[id];
    store.set("volumesFrom", saved);
    candidates = null;
    loadSeries();
  }

  function refreshVolumesPanel() {
    const old = volPanel;
    const fresh = volumesPanel();
    if (old && old.isConnected) old.replaceWith(fresh);
  }

  function volumesPanel() {
    if (source === "mangadex") return null;
    const query = h("input", { type: "search", value: siteTitle, "aria-label": "Tên truyện để tìm trên MangaDex" });
    const link = h("input", { type: "text", placeholder: "Hoặc dán link MangaDex của truyện này", "aria-label": "Link MangaDex" });
    const search = async () => {
      const text = query.value.trim();
      if (!text) { toast("Nhập tên truyện để tìm", { bad: true }); return; }
      const mine = ++searchSeq;
      searching = true; candidates = null; refreshVolumesPanel();
      try {
        const res = await api("/api/search?" + new URLSearchParams({ q: text, lang: "", order: "relevance", adult: "1", offset: "0" }));
        if (mine === searchSeq) candidates = res.items.slice(0, 8);
      } catch (err) {
        if (mine === searchSeq) { candidates = []; toast(err.message, { bad: true }); }
      } finally {
        if (mine === searchSeq) { searching = false; refreshVolumesPanel(); }
      }
    };
    const results = searching ? spinner()
      : candidates === null ? null
      : candidates.length === 0 ? h("p", { class: "muted small" }, "MangaDex không có truyện nào khớp tên này. Thử tên khác (tên gốc, tên tiếng Anh) hoặc dán link.")
      : h("div", null, candidates.map((item) => h("div", { class: "row", style: "align-items:center;margin-top:.5rem" },
          item.CoverURL ? h("img", { src: item.CoverURL, alt: "", style: "width:40px;border-radius:4px", referrerpolicy: "no-referrer" }) : null,
          h("div", { class: "grow" },
            h("div", null, item.Title || "(không tên)"),
            h("div", { class: "muted small" }, [item.Year, item.Status, (item.Authors || []).slice(0, 2).join(", ")].filter(Boolean).join(" · "))),
          h("button", { class: "btn small", onclick: () => setVolumesFrom(item.ID) }, "Dùng"))));
    volPanel = h("details", { class: "panel", open: !!volumesFrom || searching || candidates !== null },
      h("summary", null, "Chia tập theo MangaDex",
        volumesFrom ? h("span", { class: "chip ok", style: "margin-left:.5rem" }, "đang dùng") : null),
      h("p", { class: "muted small" }, "Chương và ảnh vẫn lấy từ nguồn của bạn. MangaDex chỉ cho biết chương nào thuộc tập nào, cùng tên truyện, tác giả và bìa. Chương MangaDex không biết sẽ xếp theo các chương bên cạnh."),
      h("div", { class: "row" },
        h("label", { class: "field grow" }, h("span", null, "Tìm trên MangaDex theo tên"), query),
        h("button", { class: "btn", onclick: search }, "Tìm")),
      h("div", { class: "row", style: "margin-top:.5rem" },
        h("label", { class: "field grow" }, h("span", null, "Link MangaDex"), link),
        h("button", { class: "btn", onclick: () => {
          const dexId = seriesIdFromText(link.value.trim());
          if (!dexId) { toast("Đó không phải link hoặc ID truyện MangaDex", { bad: true }); return; }
          setVolumesFrom(dexId);
        } }, "Dùng link này")),
      results,
      volumesFrom ? h("div", { class: "row", style: "margin-top:.75rem" },
        h("button", { class: "btn danger", onclick: () => setVolumesFrom("") }, "Bỏ chia tập theo MangaDex")) : null);
    return volPanel;
  }

  function fillPanel() {
    if (source !== "mangadex") return null;
    if (savedSources.length === 0) {
      return h("div", { class: "panel" },
        h("strong", null, "Bù chương thiếu từ trang khác"),
        h("p", { class: "muted small" }, "MangaDex thiếu chương thì có thể lấy bù từ một trang khác của bạn. Chưa có nguồn nào: thêm ở tab ",
          h("a", { href: "#/sources" }, "Nguồn"), " rồi quay lại đây."));
    }
    const select = h("select", { "aria-label": "Nguồn bù chương" },
      h("option", { value: "" }, "— chọn nguồn —"),
      ...savedSources.map((s) => h("option", { value: s.name }, s.name)));
    select.value = fill.source;
    const url = h("input", { type: "url", value: fill.url, "aria-label": "Địa chỉ trang truyện trên nguồn bù",
      placeholder: "Địa chỉ trang liệt kê chương của truyện này trên nguồn đó" });
    const read = () => ({ source: select.value, url: url.value.trim() });
    const need = (v) => {
      if (!v.source || !v.url) { toast("Chọn nguồn và nhập địa chỉ trang truyện trên nguồn đó", { bad: true }); return false; }
      return true;
    };
    return h("details", { class: "panel", open: fillActive() },
      h("summary", null, "Bù chương thiếu từ trang khác", linked ? h("span", { class: "chip ok", style: "margin-left:.5rem" }, "đã lưu liên kết") : null),
      h("p", { class: "muted small" }, "Chương MangaDex có thì giữ nguyên, bìa và số tập lấy từ MangaDex. Chương MangaDex thiếu sẽ lấy từ trang này, ghép theo số chương."),
      h("div", { class: "row" },
        h("label", { class: "field" }, h("span", null, "Nguồn"), select),
        h("label", { class: "field grow" }, h("span", null, "Địa chỉ trang truyện trên nguồn đó"), url)),
      h("div", { class: "row", style: "margin-top:.75rem" },
        h("button", { class: "btn", onclick: () => { const v = read(); if (need(v)) { fill = v; loadSeries(); } } }, "Áp dụng"),
        h("button", { class: "btn primary", onclick: async () => {
          const v = read();
          if (!need(v)) return;
          try {
            await api("/api/links/" + encodeURIComponent(id), { method: "PUT", body: v });
            fill = v; linked = true;
            toast("Đã lưu liên kết. Lần sau mở truyện này sẽ tự bù chương.");
            loadSeries();
          } catch (err) { toast(err.message, { bad: true }); }
        } }, "Áp dụng và lưu liên kết"),
        fillActive() || linked ? h("button", { class: "btn danger", onclick: async () => {
          try {
            if (linked) await api("/api/links/" + encodeURIComponent(id), { method: "DELETE" });
            fill = { source: "", url: "" }; linked = false;
            loadSeries();
          } catch (err) { toast(err.message, { bad: true }); }
        } }, "Bỏ nguồn bù") : null));
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

  const VOLUME_PATTERN = /^(\d{1,4}(\.\d{1,2})?|special)$/i;

  function saveOverrides() {
    const all = store.get("volumeOverrides", {});
    if (Object.keys(overrides).length) all[overrideKey] = overrides; else delete all[overrideKey];
    store.set("volumeOverrides", all);
  }

  // The chapters of the series grouped by their volume, with the moves made by hand applied.
  function effectiveVolumes() {
    const groups = new Map();
    const order = [];
    for (const vol of series.volumes) {
      for (const c of vol.chapters) {
        const v = overrides[c.id] || vol.id;
        if (!groups.has(v)) groups.set(v, []);
        groups.get(v).push({ chapter: c, index: order.length });
        order.push(c.id);
      }
    }
    const rank = (v) => (v === "Special" ? Infinity : parseFloat(v));
    return [...groups.entries()]
      .sort((a, b) => rank(a[0]) - rank(b[0]))
      .map(([id, items]) => ({
        id,
        chapters: items.sort((a, b) => {
          const x = parseFloat(a.chapter.number), y = parseFloat(b.chapter.number);
          return isNaN(x) || isNaN(y) || x === y ? a.index - b.index : x - y;
        }).map((i) => i.chapter),
      }));
  }

  function originalVolume(chapterId) {
    for (const vol of series.volumes) if (vol.chapters.some((c) => c.id === chapterId)) return vol.id;
    return null;
  }

  // Moves chapters (by id) into a volume; false if the volume is not valid.
  function moveChapters(ids, volume) {
    const text = String(volume).trim();
    if (!VOLUME_PATTERN.test(text)) {
      toast("Tập phải là một số (ví dụ 3 hoặc 3.5) hoặc Special", { bad: true });
      return false;
    }
    const target = text.toLowerCase() === "special" ? "Special" : String(parseFloat(text));
    for (const cid of ids) {
      if (originalVolume(cid) === target) delete overrides[cid]; else overrides[cid] = target;
    }
    saveOverrides();
    renderChapters();
    return true;
  }

  // "1:1-8, 2:9-17": volume, then the first and last chapter number (or one number).
  function parseVolumeRanges(text) {
    const rules = [];
    for (const part of text.split(/[,;\n]/).map((x) => x.trim()).filter(Boolean)) {
      const m = /^(\S+?)\s*:\s*(\d+(?:\.\d+)?)\s*(?:-\s*(\d+(?:\.\d+)?))?$/.exec(part);
      if (!m || !VOLUME_PATTERN.test(m[1])) return { error: `Không hiểu “${part}”. Viết kiểu 1:1-8, 2:9-17` };
      const lo = parseFloat(m[2]), hi = m[3] === undefined ? lo : parseFloat(m[3]);
      if (hi < lo) return { error: `Khoảng chương “${part}” bị ngược` };
      rules.push({ volume: m[1].toLowerCase() === "special" ? "Special" : String(parseFloat(m[1])), lo, hi });
    }
    return rules.length ? { rules } : { error: "Nhập ít nhất một khoảng, ví dụ 1:1-8, 2:9-17" };
  }

  function applyRanges(text) {
    const parsed = parseVolumeRanges(text);
    if (parsed.error) { toast(parsed.error, { bad: true }); return; }
    let moved = 0;
    for (const c of series.volumes.flatMap((v) => v.chapters)) {
      const n = parseFloat(c.number);
      const rule = isNaN(n) ? null : parsed.rules.find((r) => n >= r.lo && n <= r.hi);
      if (!rule) continue;
      if (originalVolume(c.id) === rule.volume) delete overrides[c.id]; else overrides[c.id] = rule.volume;
      moved++;
    }
    if (!moved) { toast("Không có chương nào nằm trong các khoảng đó", { bad: true }); return; }
    saveOverrides();
    renderChapters();
    toast(`Đã xếp ${moved} chương theo khoảng.`);
  }

  function applyEvenSplit(perVolume, firstVolume) {
    const n = parseInt(perVolume, 10), start = parseInt(firstVolume, 10);
    if (!(n >= 1 && n <= 500) || !(start >= 0 && start <= 9999)) { toast("Nhập số chương mỗi tập (1-500) và tập bắt đầu", { bad: true }); return; }
    const chapters = series.volumes.flatMap((v) => v.chapters)
      .filter((c) => !isNaN(parseFloat(c.number)))
      .sort((a, b) => parseFloat(a.number) - parseFloat(b.number));
    chapters.forEach((c, i) => {
      const vol = String(start + Math.floor(i / n));
      if (originalVolume(c.id) === vol) delete overrides[c.id]; else overrides[c.id] = vol;
    });
    saveOverrides();
    renderChapters();
    toast(`Đã chia ${chapters.length} chương thành ${Math.ceil(chapters.length / n)} tập.`);
  }

  function manualPanel(open) {
    const ranges = h("input", { type: "text", placeholder: "1:1-8, 2:9-17, 3:18-26", "aria-label": "Chia tập theo khoảng chương" });
    const per = h("input", { type: "number", min: "1", max: "500", value: "9", size: "4", "aria-label": "Số chương mỗi tập" });
    const start = h("input", { type: "number", min: "0", value: "1", size: "4", "aria-label": "Tập bắt đầu" });
    const target = h("input", { type: "text", placeholder: "vd: 3", size: "6", "aria-label": "Chuyển các chương đã chọn sang tập" });
    const count = Object.keys(overrides).length;
    const panel = h("details", { class: "panel", open: open || count > 0, id: "manual-panel",
      ontoggle: () => { manualOpen = panel.open; } },
      h("summary", null, "Chỉnh tập thủ công",
        count ? h("span", { class: "chip hl", style: "margin-left:.5rem" }, `${count} chương đã chuyển`) : null),
      h("p", { class: "muted small" }, "Thấy tập chưa hợp lý thì tự sửa ở đây: chia theo khoảng chương, chia đều, chuyển các chương đang chọn, hoặc kéo một chương thả vào tiêu đề của tập khác. Lựa chọn được nhớ cho truyện này và áp dụng khi tải."),
      h("div", { class: "row" },
        h("label", { class: "field grow" }, h("span", null, "Chia theo khoảng chương (tập:từ-đến)"), ranges),
        h("button", { class: "btn", onclick: () => applyRanges(ranges.value) }, "Áp dụng")),
      h("div", { class: "row", style: "margin-top:.5rem" },
        h("label", { class: "field" }, h("span", null, "Chia đều: số chương mỗi tập"), per),
        h("label", { class: "field" }, h("span", null, "bắt đầu từ tập"), start),
        h("button", { class: "btn", onclick: () => applyEvenSplit(per.value, start.value) }, "Chia đều")),
      h("div", { class: "row", style: "margin-top:.5rem" },
        h("label", { class: "field" }, h("span", null, "Chuyển các chương đã chọn sang tập"), target),
        h("button", { class: "btn", onclick: () => {
          if (selected.size === 0) { toast("Tick chọn các chương cần chuyển trước", { bad: true }); return; }
          if (moveChapters([...selected], target.value)) toast("Đã chuyển các chương đã chọn.");
        } }, "Chuyển"),
        h("button", { class: "btn danger", disabled: count === 0, onclick: () => {
          overrides = {}; saveOverrides(); renderChapters(); toast("Đã khôi phục cách chia tập mặc định.");
        } }, "Khôi phục mặc định")));
    return panel;
  }

  function renderChapters() {
    if (series.volumes.length === 0) {
      const others = (series.languages || []).filter((l) => l !== lang);
      chaptersBox.replaceChildren(h("div", { class: "empty" },
        h("p", null, `Không có chương nào tải được bằng ngôn ngữ “${lang}”.`),
        h("p", { class: "small" }, "Có thể truyện chưa có bản dịch này, hoặc các chương chỉ nằm ở trang của nhà xuất bản (liên kết ngoài) nên MangaDex không cung cấp ảnh để tải."),
        others.length ? h("p", null, "Thử ngôn ngữ khác: ",
          others.map((l) => h("button", { class: "btn small", style: "margin:.15rem", onclick: () => { lang = l; loadSeries(); } }, l))) : null));
      return;
    }
    const all = series.volumes.flatMap((v) => v.chapters);
    const filledCount = all.filter((c) => c.fromFill).length;
    const notes = (series.notes || []).map(describeNote);
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
      h("span", { class: "muted small" }, `${all.length} chương, ${effectiveVolumes().length} tập`,
        filledCount ? ` (${filledCount} chương lấy từ nguồn bù)` : ""),
      h("span", { class: "range" },
        h("span", { class: "small muted" }, "Chọn khoảng:"), from, "–", to,
        h("button", { class: "btn small", onclick: () => {
          const a = parseFloat(from.value), b = parseFloat(to.value);
          if (isNaN(a) || isNaN(b)) { toast("Nhập số chương bắt đầu và kết thúc", { bad: true }); return; }
          const [lo, hi] = a <= b ? [a, b] : [b, a];
          for (const c of all) { const n = parseFloat(c.number); if (!isNaN(n) && n >= lo && n <= hi) setChapter(c, true); }
          refresh();
        } }, "Chọn")));

    const view = effectiveVolumes();
    const list = view.map((vol) => {
      const volBox = h("input", { type: "checkbox", "aria-label": `Chọn cả tập ${vol.id}`, onclick: (e) => e.stopPropagation(),
        onchange: (e) => { vol.chapters.forEach((c) => setChapter(c, e.target.checked)); refresh(); } });
      volChecks.push({ box: volBox, chapters: vol.chapters });
      const section = h("details", { class: "volume", open: true, "data-volume": vol.id,
        ondragover: (e) => { e.preventDefault(); e.dataTransfer.dropEffect = "move"; section.classList.add("drop"); },
        ondragleave: (e) => { if (!section.contains(e.relatedTarget)) section.classList.remove("drop"); },
        ondrop: (e) => {
          e.preventDefault();
          section.classList.remove("drop");
          const dragged = e.dataTransfer.getData("text/plain");
          if (!dragged) return;
          const ids = selected.has(dragged) && selected.size > 1 ? [...selected] : [dragged];
          moveChapters(ids, vol.id);
        } },
        h("summary", null, volBox, vol.id === "Special" ? "Chưa xếp vào tập (Special)" : `Tập ${vol.id}`, h("span", { class: "muted small" }, ` · ${vol.chapters.length} chương`)),
        vol.chapters.map((c) => {
          const box = h("input", { type: "checkbox", checked: selected.has(c.id), onchange: (e) => { setChapter(c, e.target.checked); refresh(); } });
          checks.set(c.id, box);
          return h("label", { class: "chapter", draggable: "true", "data-chapter": c.id,
            ondragstart: (e) => { e.dataTransfer.setData("text/plain", c.id); e.dataTransfer.effectAllowed = "move"; } }, box,
            h("span", { class: "name" }, `Chương ${c.number}`, c.title && c.title !== c.number ? ` — ${c.title}` : "",
              c.fromFill ? h("span", { class: "chip hl", style: "margin-left:.5rem" }, "nguồn bù") : null,
              overrides[c.id] ? h("span", { class: "chip", style: "margin-left:.5rem" }, "đã chuyển") : null),
            h("span", { class: "meta" }, [c.groups.join(", "), formatDate(c.published)].filter(Boolean).join(" · ")));
        }));
      return section;
    });
    // A selector source that gives no volume information puts everything in volume 1.
    const volumeHint = source !== "mangadex" && !volumesFrom && series.volumes.length === 1 && all.length >= 10
      ? h("li", null, `Tất cả ${all.length} chương đang nằm trong một tập vì nguồn này chưa có quy tắc chia tập. Vào tab `,
          h("a", { href: "#/sources" }, "Nguồn"), ", bấm Sửa ở nguồn này rồi điền “Chia tập theo số chương” (ví dụ 1:1-8, 2:9-17), “Regex lấy số tập” hoặc “Số chương mỗi tập”.")
      : null;
    const noteBox = notes.length || volumeHint
      ? h("div", { class: "notice", style: "margin-top:1rem" },
          h("strong", null, "Cần kiểm tra:"), h("ul", { style: "margin:.4rem 0 0" }, notes.map((n) => h("li", null, n)), volumeHint))
      : null;
    const infoBox = (series.info || []).length
      ? h("p", { class: "muted small", style: "margin-top:.75rem" }, series.info.map(describeInfo).join(" "))
      : null;
    chaptersBox.replaceChildren(...nodes(manualPanel(manualOpen), toolbar, infoBox, noteBox, list));
    refresh();
  }

  function describeInfo(info) {
    const m = /^Volumes of (\d+) chapters come from the Wikipedia page "(.*)"; the rest from MangaDex$/.exec(info);
    if (m) return `Số tập của ${m[1]} chương lấy từ trang Wikipedia “${m[2]}”, phần còn lại từ MangaDex.`;
    const last = /^(\d+) chapters are not in any volume yet and were put into the last volume \((.*)\)$/.exec(info);
    return last ? `${last[1]} chương chưa thuộc tập nào đã được xếp vào tập cuối (tập ${last[2]}).` : info;
  }

  // The server reports guessed volumes in English; show them in Vietnamese.
  function describeNote(note) {
    const m = /^chapter (\S+) is not on the main source; its volume \((.*)\) is a guess/.exec(note);
    if (m) return `Chương ${m[1]} chỉ có ở nguồn bù; tập ${m[2]} là đoán vì nằm giữa hai tập khác nhau. Kiểm tra lại nếu cần.`;
    const many = /^(\d+) chapters \((\S+) to (\S+)\) are not in MangaDex's volumes, which list only (\d+) chapters in (\d+) volumes/.exec(note);
    if (many) {
      return `MangaDex chưa xếp ${many[1]} chương (từ ${many[2]} đến ${many[3]}) vào tập nên tool đoán theo các chương bên cạnh. MangaDex chỉ có thông tin tập cho ${many[4]} chương (${many[5]} tập). Nếu quá ít, truyện này trên MangaDex chưa ghi số tập: hãy chọn bản MangaDex khác hoặc chia tập bằng quy tắc ở tab Nguồn.`;
    }
    const v = /^chapter (\S+) is not in MangaDex's volumes; its volume \((.*)\) is a guess/.exec(note);
    if (v) return `MangaDex chưa xếp chương ${v[1]} vào tập; tập ${v[2]} là đoán theo các chương bên cạnh. Kiểm tra lại nếu cần.`;
    const wiki = /^Wikipedia could not supply the volumes \((.*)\)$/.exec(note);
    if (wiki) return `Không lấy được bảng tập từ Wikipedia (${wiki[1]}).`;
    if (note.startsWith("MangaDex has no volume information")) {
      return "MangaDex chưa có thông tin tập cho truyện này nên mọi chương nằm trong “Special”. Thử một truyện MangaDex khác, hoặc chia tập bằng quy tắc ở tab Nguồn.";
    }
    return note;
  }

  function updateBar() {
    bar.hidden = selected.size === 0;
    if (selected.size === 0) { bar.replaceChildren(); return; }
    const button = h("button", { class: "btn primary", onclick: startDownload }, `Tải xuống (${options.format.toUpperCase()})`);
    bar.replaceChildren(h("strong", null, `Đã chọn ${selected.size} chương`), button);
  }

  // The by-hand moves of the chapters that are about to be downloaded.
  function chosenOverrides() {
    const picked = {};
    for (const cid of selected) if (overrides[cid]) picked[cid] = overrides[cid];
    return Object.keys(picked).length ? picked : undefined;
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
        fill: fillActive() ? { source: fill.source, url: fill.url } : undefined,
        volumesFrom: volumesFrom || undefined,
        volumeOverrides: chosenOverrides(),
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
  ["image_attr", "Thuộc tính chứa URL ảnh (có thể nhiều, cách nhau bằng dấu phẩy)", "text", "data-original,data-src,src"],
  ["user_agent", "User-Agent (tuỳ chọn)", "text", ""],
  ["referer", "Referer (tuỳ chọn)", "text", ""],
  ["title", "Tên truyện (tuỳ chọn)", "text", ""],
  ["volume_ranges", "Chia tập theo số chương (tuỳ chọn)", "text", "1:1-8, 2:9-17, 3:18-26"],
  ["volume_regex", "Regex lấy số tập từ tiêu đề hoặc URL (tuỳ chọn)", "text", "Tập\\s*(\\d+)"],
  ["chapters_per_volume", "Số chương mỗi tập (cho chương không khớp quy tắc nào)", "number", "0"],
  ["max_concurrent_downloads", "Tải song song tối đa (1-8)", "number", "4"],
  ["timeout_seconds", "Timeout mỗi request (giây)", "number", "30"],
];

let lastDetection = null;

async function sourcesPage() {
  const saved = await api("/api/sources");
  const detection = lastDetection;
  lastDetection = null;
  const inputs = {};
  const resultBox = h("div");
  const name = h("input", { type: "text", placeholder: "vd: trangA", "aria-label": "Tên nguồn" });
  const testURL = h("input", { type: "url", placeholder: "https://…/trang-liet-ke-chuong", "aria-label": "Địa chỉ trang truyện để thử" });
  const pasted = h("textarea", { placeholder: '{"base_url": "...", "chapter_list_selector": "...", ...}', "aria-label": "JSON cấu hình" });

  const fields = SOURCE_FIELDS.map(([key, label, type, placeholder]) => {
    inputs[key] = h("input", { type, placeholder, "aria-label": label });
    return h("label", { class: "field" }, h("span", null, label), inputs[key]);
  });

  // Settings the form has no field for (the catalogue ones) are kept when a source is edited.
  let extraConfig = {};
  function readConfig() {
    const cfg = Object.assign({}, extraConfig);
    for (const [key, , type] of SOURCE_FIELDS) {
      const v = inputs[key].value.trim();
      if (v === "") continue;
      cfg[key] = type === "number" ? Number(v) : v;
    }
    return cfg;
  }
  function fillConfig(cfg) {
    const known = new Set(SOURCE_FIELDS.map(([key]) => key));
    extraConfig = {};
    for (const [key, value] of Object.entries(cfg || {})) if (!known.has(key) && value !== "" && value !== 0) extraConfig[key] = value;
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

  // --- automatic detection from a home page ---
  const scanURL = h("input", { type: "url", placeholder: "https://ten-mien-cua-ban.com", "aria-label": "Địa chỉ trang chủ của website" });
  const rights = h("input", { type: "checkbox", onchange: sync });
  const scanButton = h("button", { class: "btn primary", disabled: true, onclick: scan }, "Quét và lưu nguồn");
  const scanBox = h("div");
  function sync() { scanButton.disabled = !(rights.checked && scanURL.value.trim() !== ""); }
  scanURL.addEventListener("input", sync);

  async function scan() {
    scanButton.disabled = true;
    scanBox.replaceChildren(spinner(), h("p", { class: "muted small", style: "text-align:center" }, "Đang mở vài trang của website để tìm cấu trúc…"));
    try {
      const res = await api("/api/sources/detect", { method: "POST", timeout: 120000, body: { url: scanURL.value.trim(), confirmRights: rights.checked } });
      const host = new URL(res.config.base_url).hostname;
      const sourceName = (name.value.trim() || host.replace(/^www\./, "").replace(/[^A-Za-z0-9_-]/g, "-")).slice(0, 40);
      await api("/api/sources", { method: "POST", body: { name: sourceName, config: res.config } });
      lastDetection = Object.assign({ name: sourceName }, res);
      toast(`Đã lưu nguồn “${sourceName}”.`);
      render();
    } catch (err) {
      scanBox.replaceChildren(errorBox(err));
      sync();
    }
  }

  const summary = detection ? h("div", { class: "panel" },
    h("strong", null, `Đã nhận diện và lưu nguồn “${detection.name}”`),
    h("ul", null,
      h("li", null, `Trang truyện dùng để kiểm tra: ${detection.seriesUrl} (${detection.chapterCount} chương)`),
      h("li", null, `Trang chương mẫu: ${detection.chapterUrl} (${detection.imageCount} ảnh)`),
      h("li", null, h("code", null, `chapter_list_selector: ${detection.config.chapter_list_selector}`)),
      h("li", null, h("code", null, `image_list_selector: ${detection.config.image_list_selector}`), " · ", h("code", null, `image_attr: ${detection.config.image_attr}`)),
      detection.notes.map((n) => h("li", { class: "muted" }, n))),
    detection.seriesCount
      ? h("p", null, `Nguồn này duyệt được như MangaDex: tìm thấy ${detection.seriesCount} truyện ở trang vừa quét${detection.config.search_url ? ", có cả ô tìm kiếm" : ""}. `,
          h("button", { class: "btn primary small", onclick: () => {
            const st = Object.assign({ lang: "vi", order: "", adult: false }, store.get("browse", {}));
            st.source = detection.name; store.set("browse", st); location.hash = "#/";
          } }, "Duyệt truyện của nguồn này"))
      : h("p", null, "Để dùng: vào ", h("a", { href: "#/" }, "Duyệt truyện"), `, chọn nguồn “${detection.name}” rồi dán địa chỉ một truyện của website.`)) : null;

  app.append(
    h("h1", null, "Nguồn"),
    h("p", { class: "muted" }, "Ngoài MangaDex, bạn có thể thêm trang web của mình. Chỉ dùng với trang mà điều khoản cho phép tải tự động và nội dung bạn có quyền đọc."),
    summary,
    list,
    h("h2", { style: "margin-top:2rem" }, "Tự nhận diện từ trang chủ"),
    h("div", { class: "panel" },
      h("p", { class: "muted small", style: "margin-top:0" },
        "Chỉ cần địa chỉ trang chủ (hoặc trang của một truyện). Công cụ đọc robots.txt, mở khoảng 3 đến 6 trang, tự tìm danh sách chương, ảnh và thuộc tính ảnh, kiểm tra thử rồi lưu nguồn. Chỉ đọc được HTML thuần, không vượt qua trang kiểm tra trình duyệt (anti-bot)."),
      h("div", { class: "row" }, h("label", { class: "field grow" }, h("span", null, "Địa chỉ trang chủ"), scanURL)),
      h("label", { class: "check", style: "margin-top:.6rem" }, rights, "Tôi có quyền dùng nội dung của website này và điều khoản của website cho phép tải tự động"),
      h("div", { style: "margin-top:.75rem" }, scanButton),
      scanBox),
    h("h2", { style: "margin-top:2rem" }, "Thêm hoặc sửa nguồn thủ công"),
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
