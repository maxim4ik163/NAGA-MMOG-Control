"use strict";
// Naga Control UI. State comes from /api/state; live status arrives over SSE.
// Edits go into a draft copy of the data and are saved with one request.

const TOKEN = new URLSearchParams(location.search).get("t") || "";
let S = null;        // last server state {data, status, keys, lang}
let D = null;        // draft data (edited)
let dirty = false;
let tab = "buttons";
let scope = "global"; // "global" or a profile id
let layer = "123";
let sel = "123:1";
let capture = null;   // key capture state for the combo field
let calib = { result: null, busy: false, last: 0 };
let mSel = null;      // id of the macro open in the Macros tab
let evSel = -1;       // selected action row of that macro
let rec = null;       // macro recorder state
let LAYOUTS = null;   // installed keyboard layouts [{id, name}]

let LANG = "ru";
const L = (ru, en) => (LANG === "ru" ? ru : en);

// ---------------------------------------------------------------------------
// helpers

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "value") el.value = v;
    else if (k === "checked") el.checked = !!v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of kids.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

async function api(path, body) {
  const opt = { method: body === undefined ? "GET" : "POST", headers: { "X-Token": TOKEN } };
  if (body !== undefined) { opt.body = JSON.stringify(body); opt.headers["Content-Type"] = "application/json"; }
  const r = await fetch("/api/" + path, opt);
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || r.statusText);
  return j;
}

function toast(text, err) {
  const t = h("div", { class: err ? "err" : "" }, text);
  document.getElementById("toast").append(t);
  setTimeout(() => t.remove(), err ? 7000 : 3000);
}

const clone = (o) => JSON.parse(JSON.stringify(o));
function markDirty() { dirty = true; renderEditbar(); }

function modal(build) {
  const m = document.getElementById("modal"), box = document.getElementById("modalBox");
  box.innerHTML = "";
  const close = () => m.classList.add("hidden");
  box.append(...[].concat(build(close)));
  m.classList.remove("hidden");
  m.onclick = (e) => { if (e.target === m) close(); };
}

// ---------------------------------------------------------------------------
// bindings

function slotNum(slot) { return slot.split(":")[1]; }
const DEFAULTS = {
  "123": ["1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "-", "="],
  "num": ["Num1", "Num2", "Num3", "Num4", "Num5", "Num6", "Num7", "Num8", "Num9", "Num0", "Num-", "Num+"],
};
function defaultText(slot) {
  const [l, n] = slot.split(":");
  if (l === "m") return n === "4" ? L("Кнопка 4 (назад)", "Button 4 (back)") : L("Кнопка 5 (вперёд)", "Button 5 (forward)");
  return DEFAULTS[l][+n - 1];
}
function normalize(d) {
  d.profiles = d.profiles || [];
  d.macros = d.macros || [];
  for (const m of d.macros) m.events = m.events || [];
  for (const p of d.profiles) { p.exes = p.exes || []; p.bindings = p.bindings || {}; }
  d.settings.global = d.settings.global || {};
  return d;
}
function profileById(id) { return D.profiles.find((p) => p.id === id); }

function bindingText(b, slot) {
  switch (b.kind) {
    case "default": return defaultText(slot);
    case "keys": return b.keys;
    case "disable": return L("Отключено", "Disabled");
    case "dpi-up": return L("DPI: следующая", "DPI: next");
    case "dpi-down": return L("DPI: предыдущая", "DPI: previous");
    case "dpi-cycle": return L("DPI: по кругу", "DPI: cycle");
    case "dpi-hold": return L("DPI ", "DPI ") + L("ступень ", "stage ") + b.stage + L(" (удерж.)", " (hold)");
    case "macro": { const m = macroById(b.macroId); return L("Макрос: ", "Macro: ") + (m ? m.name : L("(удалён)", "(deleted)")); }
    case "text": return b.secret ? L("Текст: ••••••", "Text: ••••••") : L("Текст: ", "Text: ") + (b.text || "").replace(/\s+/g, " ").slice(0, 40);
    case "run": return L("Запуск: ", "Start: ") + baseName(b.path || "");
    case "command": return "Win+R: " + (b.cmd || "");
  }
  return "";
}
function baseName(p) { return p.replace(/^.*[\\/]/, "") || p; }
function macroById(id) { return ((D && D.macros) || []).find((m) => m.id === id); }

// effective binding for a slot in a scope: {b, src}
function effective(slot, sc) {
  const g = D.settings.global[slot];
  const hasG = g && g.kind;
  if (sc !== "global") {
    if (hasG && g.force) return { b: g, src: "global" };
    const p = profileById(sc);
    const pb = p && p.bindings[slot];
    if (pb && pb.kind) return { b: pb, src: "profile" };
  }
  if (hasG) return { b: g, src: "global" };
  return { b: { kind: "default" }, src: "default" };
}
function ownBinding(slot, sc) {
  if (sc === "global") return D.settings.global[slot] || { kind: "" };
  const p = profileById(sc);
  return (p && p.bindings[slot]) || { kind: "" };
}
function setOwn(slot, sc, b) {
  const m = sc === "global" ? D.settings.global : profileById(sc).bindings;
  if (!b.kind && !b.force) delete m[slot]; else m[slot] = b;
  markDirty();
}

// combo parsing mirrors keys.go (validated again on save)
function keyByName(n) {
  n = n.trim().toLowerCase();
  const alias = { control: "ctrl", ctl: "ctrl", meta: "win", windows: "win", super: "win", escape: "esc", del: "delete", ins: "insert",
    return: "enter", arrowleft: "left", arrowright: "right", arrowup: "up", arrowdown: "down", "←": "left", "→": "right", "↑": "up", "↓": "down", pgup: "pageup", pgdn: "pagedown" };
  n = alias[n] || n;
  return S.keys.find((k) => k.name.toLowerCase() === n);
}
function parseCombo(s) {
  const parts = [];
  let cur = "";
  for (const ch of s) {
    if (ch === "+" && cur !== "" && cur.trim().toLowerCase() !== "num") { parts.push(cur.trim()); cur = ""; continue; }
    cur += ch;
  }
  if (cur.trim()) parts.push(cur.trim());
  if (!parts.length) return null;
  const mods = { Ctrl: false, Shift: false, Alt: false, Win: false };
  const keys = [];
  for (const p of parts) {
    const k = keyByName(p);
    if (!k) return null;
    if (k.mod) mods[k.name] = true;
    else if (!keys.includes(k.name)) keys.push(k.name);
  }
  const out = ["Ctrl", "Shift", "Alt", "Win"].filter((m) => mods[m]).concat(keys);
  return out.length ? out.join("+") : null;
}

// ---------------------------------------------------------------------------
// header, banner, tabs, edit bar

function renderHeader() {
  const st = S.status;
  const dev = document.getElementById("device");
  dev.innerHTML = "";
  dev.append(
    h("span", { class: "chip " + (st.mouse ? "on" : "off") }, h("i", { class: "dot " + (st.mouse ? "ok" : "") }),
      st.mouse ? h("span", {}, "Razer Naga", st.fw ? h("span", { class: "muted" }, " · " + L("прошивка ", "firmware ") + st.fw) : "") : L("Мышь не найдена", "Mouse not found")),
    h("span", { class: "chip " + (st.pad === "ready" ? "on" : "off") }, h("i", { class: "dot " + (st.pad === "ready" ? "ok" : st.pad === "absent" ? "" : "bad") }),
      { ready: L("Боковая панель", "Side keypad"), keyboard: L("Панель: нужен WinUSB", "Keypad: needs WinUSB"), error: L("Панель: ошибка", "Keypad: error"), absent: L("Панель не найдена", "Keypad not found") }[st.pad] || st.pad),
  );
  if (st.mouse) dev.append(h("span", { class: "chip on" }, h("b", {}, (st.hold || st.dpi) + " DPI"), st.poll ? h("span", { class: "muted" }, " · " + st.poll + " " + L("Гц", "Hz")) : ""));
  const cur = document.getElementById("current");
  cur.innerHTML = "";
  cur.append(h("div", {}, L("Профиль: ", "Profile: "), h("b", {}, st.activeName || L("нет (глобальные)", "none (global)"))),
    h("div", {}, st.foreground ? L("Активное окно: ", "Active window: ") + st.foreground : ""));
}

function renderBanner() {
  const b = document.getElementById("banner");
  b.innerHTML = "";
  const st = S.status;
  if (st.mouse && st.pad === "keyboard") {
    b.append(h("div", { class: "bn warn" }, h("span", {}, L("Боковая панель пока работает как обычная клавиатура: приложение не может её переназначить. Установите на неё драйвер WinUSB — см. «Настройки → Боковая панель».",
      "The side keypad still works as a plain keyboard, so the app cannot remap it. Install the WinUSB driver on it — see Settings → Side keypad.")),
      h("button", { class: "small", onclick: () => goTab("settings") }, L("Как настроить", "How to set up"))));
  } else if (st.pad === "error") {
    b.append(h("div", { class: "bn err" }, h("span", {}, L("Не удалось открыть боковую панель: ", "Cannot open the side keypad: ") + (st.padErr || ""))));
  }
  if (!st.hook) {
    b.append(h("div", { class: "bn err" }, h("span", {}, L("Хук мыши не работает: кнопки 4/5 и фильтр дребезга недоступны.", "The mouse hook is not running: buttons 4/5 and the chatter filter are unavailable."))));
  }
  if (st.devErr && !st.mouse && st.devErr !== "the mouse was disconnected") {
    b.append(h("div", { class: "bn info" }, h("span", {}, L("Мышь: ", "Mouse: ") + st.devErr)));
  }
}

const TABS = [
  ["buttons", () => L("Кнопки", "Buttons")],
  ["profiles", () => L("Профили", "Profiles")],
  ["macros", () => L("Макросы", "Macros")],
  ["mouse", () => L("DPI и подсветка", "DPI & lights")],
  ["chatter", () => L("Дребезг ЛКМ", "Left-click chatter")],
  ["settings", () => L("Настройки", "Settings")],
];

function goTab(id) {
  if (id !== "macros") stopRec();
  const changed = tab !== id;
  tab = id;
  renderAll();
  if (changed) document.getElementById("view").scrollTop = 0;
}

function renderTabs() {
  const n = document.getElementById("tabs");
  n.innerHTML = "";
  for (const [id, name] of TABS) n.append(h("button", { class: tab === id ? "on" : "", onclick: () => goTab(id) }, name()));
  n.append(h("div", { class: "navfoot" }, "Naga Control " + S.status.version, h("br"),
    h("a", { href: "#", onclick: (e) => { e.preventDefault(); api("open", { what: "repo" }); } }, "GitHub")));
}

function renderEditbar() {
  const b = document.getElementById("editbar");
  b.innerHTML = "";
  b.append(h("span", { class: "grow" }), dirty ? h("span", { class: "dirty" }, L("Есть несохранённые изменения", "Unsaved changes")) : h("span", { class: "pend" }, L("Все изменения сохранены", "All changes saved")),
    h("button", { disabled: !dirty, onclick: () => { stopRec(); D = normalize(clone(S.data)); dirty = false; renderAll(); } }, L("Отменить", "Revert")),
    h("button", { class: "primary", disabled: !dirty, onclick: save }, L("Сохранить", "Save")));
}

async function save() {
  stopRec();
  try {
    const r = await api("save", D);
    S = r; D = normalize(clone(r.data)); dirty = false;
    if (r.lang && r.lang !== LANG) LANG = r.lang;
    toast(L("Сохранено", "Saved"));
    renderAll();
  } catch (e) { toast(e.message, true); }
}

// ---------------------------------------------------------------------------
// Buttons tab

function scopeSelect() {
  const s = h("select", { onchange: (e) => { scope = e.target.value; renderView(); } },
    h("option", { value: "global" }, L("Глобальные назначения", "Global bindings")),
    ...D.profiles.map((p) => h("option", { value: p.id }, L("Профиль: ", "Profile: ") + p.name)));
  s.value = profileById(scope) ? scope : "global";
  scope = s.value;
  return s;
}

function keyTile(slot, label) {
  const e = effective(slot, scope);
  const own = ownBinding(slot, scope);
  const inherited = scope !== "global" ? e.src !== "profile" : e.src === "default";
  const src = { global: L("глобальное", "global"), profile: L("профиль", "profile"), default: L("по умолчанию", "default") }[e.src];
  return h("button", { class: "pkey" + (sel === slot ? " sel" : "") + (inherited ? " inh" : "") + (e.src === "global" && e.b.force ? " force" : ""),
    onclick: () => { sel = slot; capture = null; renderView(); } },
    h("span", { class: "num" }, label), h("span", { class: "act" }, bindingText(e.b, slot)),
    h("span", { class: "src" }, own.kind || own.force ? "" : src));
}

// ---------------------------------------------------------------------------
// Picture of the mouse (left side) with the buttons to click on

const SVGNS = "http://www.w3.org/2000/svg";
function svg(tag, attrs, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v); else el.setAttribute(k, v);
  }
  for (const c of kids.flat()) if (c !== null && c !== undefined && c !== false) el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  return el;
}

// one clickable button of the picture
function hotKey(slot, x, y, w, hgt, label, rot) {
  const e = effective(slot, scope);
  const own = ownBinding(slot, scope);
  const set = !!(own.kind || own.force);
  const cls = "hk" + (sel === slot ? " sel" : "") + (set ? " own" : "") + (e.b.kind !== "default" ? " mapped" : "");
  const tip = (slot.startsWith("m:") ? L("Кнопка ", "Button ") + slot.slice(2) : L("Кнопка ", "Button ") + slotNum(slot) + " (" + (layer === "123" ? "123" : "NUM") + ")") + ": " + bindingText(e.b, slot);
  return svg("g", { class: cls, transform: rot ? `rotate(${rot} ${x + w / 2} ${y + hgt / 2})` : null, onclick: () => { sel = slot; capture = null; renderView(); } },
    svg("title", {}, tip),
    svg("rect", { x, y, width: w, height: hgt, rx: Math.min(6, hgt / 2.6) }),
    svg("text", { x: x + w / 2, y: y + hgt / 2 + 4, "text-anchor": "middle" }, label));
}

function mousePicture() {
  const body = "M30 150 C22 118 42 92 82 80 C142 62 202 28 264 32 C320 36 348 84 342 140 C339 172 320 193 290 196 L62 196 C40 196 32 178 30 150 Z";
  const keys = [];
  const kw = 28, kh = 22, gap = 4, x0 = 154, y0 = 82;
  for (let i = 0; i < 12; i++) {
    const c = i % 3, r = Math.floor(i / 3);
    keys.push(hotKey(layer + ":" + (i + 1), x0 + c * (kw + gap), y0 + r * (kh + gap), kw, kh, i + 1));
  }
  return h("div", { class: "mousepic" }, svg("svg", { viewBox: "0 0 370 214", role: "img", "aria-label": L("Мышь Razer Naga, вид слева", "Razer Naga mouse, left side") },
    svg("defs", {}, svg("linearGradient", { id: "nbody", x1: 0, y1: 0, x2: 0, y2: 1 },
      svg("stop", { offset: "0", "stop-color": "#2f3338" }), svg("stop", { offset: "1", "stop-color": "#17191c" }))),
    svg("path", { d: body, class: "mbody", fill: "url(#nbody)" }),
    svg("path", { d: "M42 112 C90 92 150 72 210 52", class: "mseam" }),
    svg("path", { d: "M40 150 C80 156 120 160 150 192", class: "mseam" }),
    svg("rect", { x: 116, y: 58, width: 24, height: 12, rx: 6, class: "mwheel", transform: "rotate(-17 128 64)" }),
    svg("rect", { x: 146, y: 74, width: 104, height: 108, rx: 12, class: "mplate" }),
    svg("text", { x: 198, y: 205, class: "mcap", "text-anchor": "middle" }, L("слой ", "layer ") + (layer === "123" ? "123" : "NUM")),
    hotKey("m:4", 52, 84, 24, 12, "4", -22),
    hotKey("m:5", 80, 74, 24, 12, "5", -18),
    ...keys));
}

function renderButtons(v) {
  if (!sel.startsWith(layer + ":") && !sel.startsWith("m:")) sel = layer + ":1";
  const grid = h("div", { class: "pad" });
  for (let i = 1; i <= 12; i++) grid.append(keyTile(layer + ":" + i, String(i)));
  const left = h("div", { class: "card" },
    h("div", { class: "row" }, scopeSelect()),
    mousePicture(),
    h("div", { class: "row" }, h("div", { class: "seg" },
      h("button", { class: layer === "123" ? "on" : "", onclick: () => { layer = "123"; sel = "123:" + slotNum(sel.startsWith("m:") ? "123:1" : sel); renderView(); } }, L("Слой 123", "Layer 123")),
      h("button", { class: layer === "num" ? "on" : "", onclick: () => { layer = "num"; sel = "num:" + slotNum(sel.startsWith("m:") ? "num:1" : sel); renderView(); } }, L("Слой NUM", "Layer NUM"))),
      h("span", { class: "muted small" }, L("по переключателю снизу мыши", "set by the switch under the mouse"))),
    h("h3", {}, L("Боковая панель", "Side keypad")), grid,
    h("h3", {}, L("Кнопки у левой кнопки мыши", "Buttons next to the left button")),
    h("div", { class: "mrow" }, keyTile("m:4", L("Кнопка 4", "Button 4")), keyTile("m:5", L("Кнопка 5", "Button 5"))),
    h("div", { class: "legend" }, h("span", {}, L("серым — унаследовано", "grey — inherited")), h("span", {}, "★ " + L("глобальное поверх профилей", "global over profiles"))));
  v.append(h("div", { class: "padlayout" }, left, renderSlotEditor()));
}

function renderSlotEditor() {
  const slot = sel;
  const own = clone(ownBinding(slot, scope));
  const isGlobal = scope === "global";
  const card = h("div", { class: "card" });
  const title = slot.startsWith("m:") ? (slot === "m:4" ? L("Кнопка 4", "Button 4") : L("Кнопка 5", "Button 5"))
    : L("Кнопка ", "Button ") + slotNum(slot) + " · " + (slot.startsWith("123") ? L("слой 123", "layer 123") : L("слой NUM", "layer NUM"));
  card.append(h("h2", {}, title));
  const e = effective(slot, scope);
  card.append(h("div", { class: "muted small" }, L("Сейчас: ", "Now: ") + bindingText(e.b, slot) + " (" +
    { global: L("глобальное", "global"), profile: L("профиль", "profile"), default: L("по умолчанию", "default") }[e.src] + ")"));
  card.append(h("div", { class: "sep" }));

  const set = (b) => { setOwn(slot, scope, b); renderView(); };
  const kind = own.kind || "";
  const radio = (k, text, extra) => h("div", {},
    h("label", { class: "check" }, h("input", { type: "radio", name: "act", checked: kind === k, onchange: () => {
      const b = { kind: k, force: own.force };
      if (k === "keys") b.keys = own.keys || "";
      if (k === "dpi-hold") b.stage = own.stage || 1;
      if (k === "macro") b.macroId = (D.macros[0] || {}).id || "";
      if (k === "text") Object.assign(b, { text: "", method: "unicode", after: "", charDelay: 0 });
      if (k === "run") Object.assign(b, { path: "", args: "", show: "" });
      if (k === "command") Object.assign(b, { cmd: "", show: "" });
      set(b);
    } }), h("span", {}, text)), kind === k ? extra : null);

  const acts = h("div", { class: "acts" });
  acts.append(radio("", isGlobal ? L("Не задано (по умолчанию: ", "Not set (default: ") + defaultText(slot) + ")"
    : L("Как в глобальных", "Same as global")));
  acts.append(radio("default", (slot.startsWith("m:") ? L("Как без приложения: ", "As without the app: ") : L("Исходная клавиша: ", "Original key: ")) + defaultText(slot)));
  acts.append(radio("keys", L("Клавиша или сочетание", "Key or combination"), comboEditor(slot, own)));
  acts.append(radio("dpi-up", L("DPI: следующая ступень", "DPI: next stage")));
  acts.append(radio("dpi-down", L("DPI: предыдущая ступень", "DPI: previous stage")));
  acts.append(radio("dpi-cycle", L("DPI: по кругу", "DPI: cycle")));
  acts.append(radio("dpi-hold", L("DPI на время удержания", "DPI while held"), h("div", { class: "sub" }, L("ступень ", "stage "),
    h("select", { onchange: (ev) => { own.stage = +ev.target.value; set(own); } },
      ...[1, 2, 3, 4, 5, 6, 7, 8].map((n) => h("option", { value: n, selected: (own.stage || 1) === n }, n))))));
  acts.append(radio("macro", L("Макрос", "Macro"), macroEditor(slot, own)));
  acts.append(radio("text", L("Ввод текста", "Type a text"), textEditor(slot, own)));
  acts.append(radio("run", L("Запуск программы, файла, папки, сайта", "Start a program, file, folder, website"), runEditor(slot, own)));
  acts.append(radio("command", L("Команда, как в Win+R", "Command, as in Win+R"), commandEditor(slot, own)));
  acts.append(radio("disable", L("Отключено", "Disabled")));
  card.append(acts);
  if (isGlobal) {
    card.append(h("div", { class: "sep" }), h("label", { class: "check" },
      h("input", { type: "checkbox", checked: !!own.force, disabled: !own.kind, onchange: (ev) => { own.force = ev.target.checked; set(own); } }),
      h("span", {}, L("Всегда, поверх профилей", "Always, over profiles"), h("small", {}, L("Например Ctrl+Win+стрелки для рабочих столов: работают в любой программе.", "E.g. Ctrl+Win+arrows for virtual desktops: work in every program.")))));
  }
  return card;
}

// --- macro, text, start, command -------------------------------------------

function macroEditor(slot, own) {
  if (!D.macros.length) {
    return h("div", { class: "sub" }, h("span", { class: "muted small" }, L("Макросов пока нет. ", "No macros yet. ")),
      h("button", { class: "small", onclick: () => { const m = newMacro(); D.macros.push(m); own.macroId = m.id; setOwn(slot, scope, own); mSel = m.id; goTab("macros"); } }, L("Создать макрос", "Create a macro")));
  }
  const m = macroById(own.macroId);
  return h("div", { class: "sub" },
    h("div", { class: "row" },
      h("select", { onchange: (e) => { own.macroId = e.target.value; setOwn(slot, scope, own); renderView(); } },
        !m ? h("option", { value: "", selected: true }, L("— выберите —", "— choose —")) : null,
        ...D.macros.map((x) => h("option", { value: x.id, selected: x.id === own.macroId }, x.name))),
      h("button", { class: "small", onclick: () => { mSel = own.macroId || (D.macros[0] || {}).id; goTab("macros"); } }, L("Открыть в «Макросах»", "Open in Macros"))),
    m ? h("div", { class: "muted small" }, m.events.length + L(" действий · ", " actions · ") + modeText(m)) : null);
}

function textEditor(slot, own) {
  const upd = () => setOwn(slot, scope, own);
  const stored = own.secret && own.enc && !own.text;
  const field = own.secret
    ? h("input", { type: "password", value: own.text || "", autocomplete: "new-password", style: "width:100%",
        placeholder: stored ? L("•••••• сохранён зашифрованным — введите новый, чтобы заменить", "•••••• stored encrypted — type a new one to replace it") : L("пароль или другой секрет", "a password or another secret"),
        oninput: (e) => { own.text = e.target.value; upd(); } })
    : h("textarea", { rows: 3, style: "width:100%;resize:vertical", placeholder: L("Текст, который будет напечатан", "The text to type"), oninput: (e) => { own.text = e.target.value; upd(); } }, own.text || "");
  const method = own.method || "unicode";
  if (method === "keys" && LAYOUTS === null) loadLayouts();
  const layouts = LAYOUTS || [];
  return h("div", { class: "sub tedit" },
    field,
    h("label", { class: "check" }, h("input", { type: "checkbox", checked: !!own.secret, onchange: (e) => {
      own.secret = e.target.checked;
      if (!own.secret && own.enc && !own.text) toast(L("Сохранённый секрет не показывается — введите текст заново.", "A stored secret is never shown — enter the text again."));
      upd(); renderView();
    } }), h("span", {}, L("Секрет (пароль)", "Secret (password)"), h("small", {}, L("Хранится зашифрованным для вашей учётной записи Windows (DPAPI), в окне не показывается, на другом ПК не расшифруется.",
      "Kept encrypted for your Windows account (DPAPI), never shown in this window, cannot be decrypted on another PC.")))),
    h("div", { class: "row" }, h("span", { class: "muted small" }, L("Как печатать", "How to type")),
      h("div", { class: "seg" },
        h("button", { class: method === "unicode" ? "on" : "", onclick: () => { own.method = "unicode"; own.layout = ""; upd(); renderView(); } }, L("Символами (Unicode)", "As characters (Unicode)")),
        h("button", { class: method === "keys" ? "on" : "", onclick: () => { own.method = "keys"; upd(); renderView(); } }, L("Клавишами в раскладке", "As keys in a layout")))),
    method === "keys" ? h("div", { class: "row" }, h("span", { class: "muted small" }, L("Раскладка", "Layout")),
      h("select", { onchange: (e) => { own.layout = e.target.value; upd(); } },
        h("option", { value: "", selected: !own.layout }, L("текущая раскладка окна", "the window's current layout")),
        ...layouts.map((l) => h("option", { value: l.id, selected: (own.layout || "").toUpperCase() === l.id }, l.name)),
        own.layout && !layouts.some((l) => l.id === (own.layout || "").toUpperCase()) ? h("option", { value: own.layout, selected: true }, own.layout + L(" (не установлена)", " (not installed)")) : null)) : null,
    h("p", { class: "muted small" }, method === "keys"
      ? L("Нажимает настоящие клавиши, переключив окно на выбранную раскладку и вернув прежнюю после ввода. Для программ, которые не принимают Unicode-ввод: игры, удалённые рабочие столы, консоли, окна входа. Caps Lock учитывается.",
        "Presses real keys after switching the window to the chosen layout, and switches back afterwards. For programs that ignore Unicode input: games, remote desktops, consoles, login windows. Caps Lock is taken into account.")
      : L("Печатает символы напрямую — не зависит от раскладки и Caps Lock. Подходит почти везде.", "Types the characters directly — independent of the layout and Caps Lock. Works almost everywhere.")),
    h("div", { class: "row" },
      h("div", { class: "field" }, h("span", {}, L("Затем нажать", "Then press")),
        h("input", { type: "text", value: own.after || "", placeholder: L("ничего; например Enter", "nothing; e.g. Enter"), style: "width:160px",
          oninput: (e) => { own.after = e.target.value; e.target.classList.toggle("bad", !!e.target.value.trim() && !parseCombo(e.target.value)); upd(); },
          onblur: (e) => { const c = parseCombo(e.target.value); if (c) { own.after = c; e.target.value = c; upd(); } } })),
      h("div", { class: "field" }, h("span", {}, L("Пауза между символами, мс", "Delay between characters, ms")),
        h("input", { type: "number", min: 0, max: 500, value: own.charDelay || 0, onchange: (e) => { own.charDelay = Math.max(0, Math.min(500, +e.target.value | 0)); e.target.value = own.charDelay; upd(); } }))));
}

const SHOW = () => [["", L("обычное окно", "normal window")], ["min", L("свёрнутым", "minimised")], ["max", L("развёрнутым", "maximised")], ["hidden", L("скрытым (без окна)", "hidden (no window)")]];
function showSelect(own, upd) {
  return h("div", { class: "field" }, h("span", {}, L("Окно", "Window")),
    h("select", { onchange: (e) => { own.show = e.target.value; upd(); } }, ...SHOW().map(([v, t]) => h("option", { value: v, selected: (own.show || "") === v }, t))));
}

function runEditor(slot, own) {
  const upd = () => setOwn(slot, scope, own);
  const path = h("input", { type: "text", value: own.path || "", placeholder: "C:\\Program Files\\…\\app.exe", style: "flex:1;min-width:220px", oninput: (e) => { own.path = e.target.value; upd(); } });
  const browse = window.nagaPickFile ? h("button", { class: "small", onclick: () => {
    window.nagaPickResult = (p) => { if (p) { own.path = p; upd(); renderView(); } };
    window.nagaPickFile(own.path || "", LANG);
  } }, L("Обзор…", "Browse…")) : null;
  return h("div", { class: "sub tedit" },
    h("div", { class: "row" }, path, browse),
    h("div", { class: "row" },
      h("div", { class: "field" }, h("span", {}, L("Аргументы", "Arguments")),
        h("input", { type: "text", value: own.args || "", style: "width:240px", oninput: (e) => { own.args = e.target.value; upd(); } })),
      showSelect(own, upd)),
    h("p", { class: "muted small" }, L("Программа, ярлык, документ, папка или адрес сайта. Переменные вида %USERPROFILE% раскрываются.",
      "A program, shortcut, document, folder or website address. Variables like %USERPROFILE% are expanded.")));
}

const CMD_EXAMPLES = ["%LOCALAPPDATA%", "%TEMP%", "shell:startup", "cmd /k ipconfig /all", "control", "ms-settings:display", "taskmgr", "notepad"];
function commandEditor(slot, own) {
  const upd = () => setOwn(slot, scope, own);
  const input = h("input", { type: "text", value: own.cmd || "", placeholder: "%LOCALAPPDATA%", class: "mono", style: "width:100%", oninput: (e) => { own.cmd = e.target.value; upd(); } });
  return h("div", { class: "sub tedit" },
    input,
    h("div", { class: "quick" }, ...CMD_EXAMPLES.map((c) => h("button", { onclick: () => { own.cmd = c; upd(); renderView(); } }, c))),
    h("div", { class: "row" }, showSelect(own, upd)),
    h("p", { class: "muted small" }, L("Выполняется так же, как из окна «Выполнить» (Win+R), но само окно не открывается: папки (%LOCALAPPDATA%, shell:startup), программы с параметрами, ссылки ms-settings:. Команды самой консоли (dir, echo, &&) запускайте через «cmd /c …» — с окном «скрытым» консоль не мелькает.",
      "Runs the same way as from the Run box (Win+R), without opening the box: folders (%LOCALAPPDATA%, shell:startup), programs with arguments, ms-settings: links. Console built-ins (dir, echo, &&) need “cmd /c …” — with the window set to hidden no console flashes.")));
}

async function loadLayouts() {
  LAYOUTS = [];
  try { LAYOUTS = await api("layouts"); } catch (e) { /* the list stays empty */ }
  if (tab === "buttons") renderView();
}

const QUICK = ["Ctrl+Win+Left", "Ctrl+Win+Right", "Ctrl+Z", "Ctrl+Y", "Ctrl+C", "Ctrl+V", "Ctrl+S", "Esc", "Enter", "Delete", "Space", "Tab", "Shift", "Ctrl", "Alt"];

function comboEditor(slot, own) {
  const wrap = h("div", { class: "combo" });
  const input = h("input", { type: "text", value: own.keys || "", placeholder: "Ctrl+Shift+S", oninput: (e) => { own.keys = e.target.value; setOwn(slot, scope, own); input.classList.toggle("bad", !parseCombo(e.target.value)); },
    onblur: (e) => { const c = parseCombo(e.target.value); if (c) { own.keys = c; setOwn(slot, scope, own); renderView(); } } });
  const cap = h("button", { class: "keycap" + (capture ? " listen" : ""), onclick: () => startCapture(slot, own) },
    capture ? L("Нажмите сочетание… (Esc — отмена)", "Press the combination… (Esc cancels)") : L("Записать с клавиатуры", "Record from keyboard"));
  wrap.append(input, cap);
  const q = h("div", { class: "quick" }, ...QUICK.map((k) => h("button", { onclick: () => { own.keys = k; setOwn(slot, scope, own); renderView(); } }, k)));
  return h("div", {}, wrap, h("div", { class: "sub" }, q, h("div", { class: "muted small" }, L("Пока идёт запись, Windows не реагирует на нажатия, поэтому можно записывать и Ctrl+Win+стрелки, и Alt+Tab.",
    "While recording, Windows ignores the keys, so Ctrl+Win+arrows and Alt+Tab can be recorded too."))));
}

// The combination is captured by the app's own keyboard hook: the window
// cannot see Win combinations (Windows acts on Ctrl+Win+arrows first).
async function startCapture(slot, own) {
  if (capture) return;
  capture = { slot };
  renderView();
  try {
    const r = await api("capture", {});
    if (r.keys) { own.keys = r.keys; setOwn(slot, scope, own); }
  } catch (e) { toast(e.message, true); }
  capture = null;
  renderView();
}

// ---------------------------------------------------------------------------
// Profiles tab

function renderProfiles(v) {
  const st = S.status;
  const card = h("div", { class: "card" });
  card.append(h("h2", {}, L("Профили программ", "Program profiles")),
    h("p", { class: "muted small" }, L("Профиль включается сам, когда окно указанной программы становится активным, и выключается, когда вы переключаетесь в другую программу. Назначения профиля перекрывают глобальные, кроме отмеченных «поверх профилей».",
      "A profile turns on by itself when a window of one of its programs becomes active, and off when you switch to another program. Profile bindings override global ones, except those marked “over profiles”.")));
  if (!D.profiles.length) card.append(h("div", { class: "muted" }, L("Профилей пока нет.", "No profiles yet.")));
  D.profiles.forEach((p, i) => {
    const exes = h("div", { class: "chips" }, ...p.exes.map((x, j) => h("span", { class: "xchip" }, x,
      h("button", { title: L("убрать", "remove"), onclick: () => { p.exes.splice(j, 1); markDirty(); renderView(); } }, "×"))),
      h("button", { class: "small", onclick: () => pickProgram(p) }, "+ " + L("программа", "program")));
    const own = h("label", { class: "check" }, h("input", { type: "checkbox", checked: !!p.dpi, onchange: (e) => {
      p.dpi = e.target.checked ? clone(D.settings.dpi) : null; markDirty(); renderView(); } }), h("span", {}, L("Свои ступени DPI", "Own DPI stages")));
    card.append(h("div", { class: "prow" },
      h("input", { type: "checkbox", title: L("включён", "enabled"), checked: p.enabled, onchange: (e) => { p.enabled = e.target.checked; markDirty(); } }),
      h("div", {},
        h("div", { class: "row nm" }, h("input", { type: "text", value: p.name, oninput: (e) => { p.name = e.target.value; markDirty(); } }),
          st.active === p.id ? h("span", { class: "on-now" }, "● " + L("активен сейчас", "active now")) : ""),
        h("div", { class: "row" }, exes),
        h("div", { class: "row" }, own, h("label", { class: "check" }, h("input", { type: "checkbox", checked: p.osd, onchange: (e) => { p.osd = e.target.checked; markDirty(); } }),
          h("span", {}, L("Показывать при переключении", "Show a popup on switch")))),
        p.dpi ? h("div", { class: "row" }, stagesEditor(p.dpi)) : null),
      h("div", { class: "row" },
        h("button", { class: "small", onclick: () => { scope = p.id; goTab("buttons"); } }, L("Кнопки", "Buttons")),
        h("button", { class: "small danger", onclick: () => { if (confirm(L("Удалить профиль «", "Delete profile “") + p.name + L("»?", "”?"))) { D.profiles.splice(i, 1); markDirty(); renderView(); } } }, L("Удалить", "Delete")))));
  });
  card.append(h("div", { class: "row" }, h("button", { onclick: () => {
    D.profiles.push({ id: "p" + Date.now().toString(36), name: L("Новый профиль", "New profile"), exes: [], enabled: true, bindings: {}, dpi: null, osd: true });
    markDirty(); renderView();
    pickProgram(D.profiles[D.profiles.length - 1]);
  } }, "+ " + L("Профиль", "Profile"))));
  v.append(card);
}

async function pickProgram(p) {
  let list = [];
  try { list = await api("programs"); } catch (e) { /* ignore */ }
  modal((close) => {
    const input = h("input", { type: "text", placeholder: "acad.exe" });
    const add = (x) => {
      x = (x || "").trim().toLowerCase().replace(/^.*[\\/]/, "");
      if (!x) return;
      if (!x.endsWith(".exe")) x += ".exe";
      if (!p.exes.includes(x)) p.exes.push(x);
      if (p.name === L("Новый профиль", "New profile") || !p.name) p.name = x.replace(/\.exe$/, "");
      markDirty(); close(); renderView();
    };
    return [h("h2", {}, L("Программа для профиля", "Program for the profile")),
      h("div", { class: "row" }, input, h("button", { class: "primary", onclick: () => add(input.value) }, L("Добавить", "Add"))),
      h("h3", {}, L("Открытые сейчас окна", "Open windows")),
      h("div", { class: "plist" }, ...list.map((x) => h("div", { onclick: () => add(x) }, x))),
      h("div", { class: "foot" }, h("button", { onclick: close }, L("Закрыть", "Close")))];
  });
}

// ---------------------------------------------------------------------------
// Macros tab. Macros are part of the draft and saved with everything else.

const MOUSE_BTNS = () => [["MouseLeft", L("Левая кнопка мыши", "Left mouse button")], ["MouseRight", L("Правая кнопка мыши", "Right mouse button")],
  ["MouseMiddle", L("Средняя кнопка мыши", "Middle mouse button")], ["MouseBack", L("Кнопка мыши «назад»", "Mouse button Back")], ["MouseForward", L("Кнопка мыши «вперёд»", "Mouse button Forward")]];
const MAX_EVENTS = 5000, MAX_DELAY = 60000;

function newMacro() {
  return { id: "m" + Date.now().toString(36) + Math.floor(Math.random() * 1296).toString(36), name: L("Новый макрос", "New macro"), mode: 2, repeat: 1, events: [] };
}
function modeText(m) {
  return { 1: L("пока кнопка зажата", "while the button is held"), 2: L("выполнить ×", "run ×") + (m.repeat || 1), 3: L("до повторного нажатия", "until pressed again") }[m.mode] || "";
}
function keyLabel(k) {
  const mb = MOUSE_BTNS().find(([n]) => n === k);
  return mb ? mb[1] : k;
}
function evText(e) {
  switch (e.type) {
    case "delay": return L("Пауза", "Delay");
    case "move": return L("Курсор на ", "Cursor by ") + (e.dx || 0) + ", " + (e.dy || 0);
    case "wheel": return L("Колесо ", "Wheel ") + ((e.wheel || 0) > 0 ? L("вверх на ", "up by ") : L("вниз на ", "down by ")) + Math.abs(e.wheel || 0);
  }
  return keyLabel(e.key);
}
function evBadge(e) {
  const t = { keydown: ["down", L("нажать", "press")], keyup: ["up", L("отпустить", "release")], delay: ["delay", L("пауза", "delay")], move: ["move", L("курсор", "cursor")], wheel: ["move", L("колесо", "wheel")] }[e.type] || ["", e.type];
  return h("span", { class: "evtype " + t[0] }, t[1]);
}
function macroWarnings(m) {
  const held = new Map();
  for (const e of m.events) {
    if (e.type === "keydown") held.set(e.key, (held.get(e.key) || 0) + 1);
    if (e.type === "keyup") held.set(e.key, Math.max(0, (held.get(e.key) || 0) - 1));
  }
  const w = [];
  for (const [k, n] of held) if (n > 0) w.push(L(`«${keyLabel(k)}» нажимается, но не отпускается — программа отпустит её в конце макроса.`, `“${keyLabel(k)}” is pressed but never released — the app releases it when the macro ends.`));
  return w;
}
// every place a macro is assigned: [{map, slot, where}]
function macroUses(id) {
  const out = [];
  for (const [slot, b] of Object.entries(D.settings.global)) if (b.kind === "macro" && b.macroId === id) out.push({ map: D.settings.global, slot, where: L("Глобальные", "Global") });
  for (const p of D.profiles) for (const [slot, b] of Object.entries(p.bindings)) if (b.kind === "macro" && b.macroId === id) out.push({ map: p.bindings, slot, where: p.name });
  return out;
}
function slotTitle(slot) {
  const [l, n] = slot.split(":");
  return l === "m" ? L("кнопка ", "button ") + n : L("кнопка ", "button ") + n + " (" + (l === "123" ? "123" : "NUM") + ")";
}

function stopRec() {
  if (!rec) return;
  window.removeEventListener("keydown", rec.kd, true);
  window.removeEventListener("keyup", rec.ku, true);
  rec = null;
}

function pref(k, def) { try { const v = localStorage.getItem("naga." + k); return v === null ? def : JSON.parse(v); } catch (e) { return def; } }
function setPref(k, v) { try { localStorage.setItem("naga." + k, JSON.stringify(v)); } catch (e) { /* ignore */ } }

function renderMacros(v) {
  if (mSel && !macroById(mSel)) { mSel = null; evSel = -1; }
  if (!mSel && D.macros.length) mSel = D.macros[0].id;
  const list = h("div", { class: "mlist" }, ...D.macros.map((m) => h("div", { class: "it" + (m.id === mSel ? " on" : ""), onclick: () => { stopRec(); mSel = m.id; evSel = -1; renderView(); } },
    m.name, h("small", {}, m.events.length + L(" действий · ", " actions · ") + modeText(m)))));
  const left = h("div", {}, h("div", { class: "card" }, h("h2", {}, L("Макросы", "Macros")),
    D.macros.length ? list : h("p", { class: "muted" }, L("Макросов пока нет.", "No macros yet.")),
    h("div", { class: "row", style: "margin-top:12px" }, h("button", { class: "primary", onclick: () => { stopRec(); const m = newMacro(); D.macros.push(m); mSel = m.id; evSel = -1; markDirty(); renderView(); } }, "+ " + L("Новый макрос", "New macro"))),
    h("p", { class: "muted small" }, L("Макрос выполняет Naga Control: последовательность нажатий клавиш и кнопок мыши, паузы, движения курсора и колеса. Назначьте его на кнопку во вкладке «Кнопки».",
      "Naga Control plays the macro: a sequence of key and mouse button presses, delays, cursor and wheel moves. Assign it to a button on the Buttons tab."))));
  const m = macroById(mSel);
  v.append(h("div", { class: "mlayout" }, left, m ? macroCard(m) : h("div", { class: "card muted" }, L("Создайте макрос слева.", "Create a macro on the left."))));
  if (rec) { const t = document.querySelector(".evtable"); if (t) t.scrollTop = t.scrollHeight; }
}

function macroCard(m) {
  const mark = () => markDirty();
  const ms = L("мс", "ms");
  const rows = m.events.map((e, i) => h("tr", { class: i === evSel ? "sel" : "", onclick: (ev) => { if (ev.target.closest("input,button")) return; evSel = evSel === i ? -1 : i; renderView(); } },
    h("td", { class: "muted" }, i + 1), h("td", {}, evBadge(e)), h("td", {}, evText(e)),
    h("td", { class: "nowrap" }, h("input", { type: "number", min: 0, max: MAX_DELAY, value: e.delay || 0, onchange: (ev) => { e.delay = Math.max(0, Math.min(MAX_DELAY, +ev.target.value | 0)); ev.target.value = e.delay; mark(); } }), " " + ms),
    h("td", { class: "nowrap" },
      h("button", { class: "small", disabled: !i, onclick: () => move(i, -1) }, "↑"), " ",
      h("button", { class: "small", disabled: i >= m.events.length - 1, onclick: () => move(i, 1) }, "↓"), " ",
      h("button", { class: "small danger", onclick: () => { m.events.splice(i, 1); evSel = -1; mark(); renderView(); } }, "✕"))));
  const move = (i, d) => { const ev = m.events; [ev[i], ev[i + d]] = [ev[i + d], ev[i]]; evSel = i + d; mark(); renderView(); };
  const insert = (evs) => {
    if (m.events.length + evs.length > MAX_EVENTS) { toast(L("Слишком много действий в макросе", "Too many actions in the macro"), true); return false; }
    const at = evSel >= 0 ? evSel + 1 : m.events.length;
    m.events.splice(at, 0, ...evs);
    if (evSel >= 0) evSel += evs.length;
    mark(); renderView();
    return true;
  };

  // recording: keys anywhere in the window, mouse buttons inside the pad
  const recAdd = (type, key) => {
    const now = performance.now();
    let d = 0;
    if (rec.last) d = pref("recDelays", true) ? Math.round(now - rec.last) : pref("recFixed", 30);
    rec.last = now;
    if (!insert([{ type, key, delay: Math.min(MAX_DELAY, d) }])) stopRec();
  };
  const startRec = () => {
    if (document.activeElement) document.activeElement.blur();
    rec = { last: 0, macro: m.id };
    rec.kd = (e) => { e.preventDefault(); e.stopPropagation(); if (e.repeat) return; const k = S.keys.find((x) => x.code === e.code); if (k) recAdd("keydown", k.name); };
    rec.ku = (e) => { e.preventDefault(); e.stopPropagation(); const k = S.keys.find((x) => x.code === e.code); if (k) recAdd("keyup", k.name); };
    window.addEventListener("keydown", rec.kd, true);
    window.addEventListener("keyup", rec.ku, true);
    renderView();
  };
  const MB = { 0: "MouseLeft", 1: "MouseMiddle", 2: "MouseRight", 3: "MouseBack", 4: "MouseForward" };
  const recording = rec && rec.macro === m.id;
  const pad = h("div", { class: "recpad" + (recording ? " rec" : ""),
    oncontextmenu: (e) => e.preventDefault(), onauxclick: (e) => e.preventDefault(),
    onmousedown: (e) => { if (!recording) return; e.preventDefault(); if (MB[e.button]) recAdd("keydown", MB[e.button]); },
    onmouseup: (e) => { if (!recording) return; e.preventDefault(); if (MB[e.button]) recAdd("keyup", MB[e.button]); } },
    recording ? L("● Идёт запись: нажимайте клавиши, кликайте кнопками мыши в этой рамке. Клавишу Win записать нельзя — добавьте её вручную.", "● Recording: press keys and click mouse buttons inside this box. The Win key cannot be recorded — add it manually.")
      : L("Нажмите «Записать», затем нажимайте клавиши и кнопки мыши в этой рамке", "Press Record, then press keys and mouse buttons inside this box"));

  const keySel = h("select", { id: "akey" },
    h("optgroup", { label: L("Мышь", "Mouse") }, ...MOUSE_BTNS().map(([n, t]) => h("option", { value: n }, t))),
    h("optgroup", { label: L("Клавиатура", "Keyboard") }, ...S.keys.map((k) => h("option", { value: k.name, selected: k.name === pref("lastKey", "A") }, k.name))));
  const k = () => { setPref("lastKey", keySel.value); return keySel.value; };
  const num = (val, min, max, w) => h("input", { type: "number", min, max, value: val, style: w ? "width:" + w : null });
  const delay = num(100, 1, MAX_DELAY), dx = num(0, -10000, 10000, "70px"), dy = num(0, -10000, 10000, "70px"), wheel = num(1, -100, 100, "60px");
  const warns = macroWarnings(m);
  const uses = macroUses(m.id);

  return h("div", { class: "card" },
    h("div", { class: "row" }, h("div", { class: "field", style: "flex:1" }, h("span", {}, L("Название", "Name")),
      h("input", { type: "text", value: m.name, oninput: (e) => { m.name = e.target.value; mark(); }, onchange: () => renderView() }))),
    h("h3", {}, L("Как выполнять", "Run mode")),
    h("div", { class: "row" },
      ...[[2, L("Выполнить N раз", "Run N times")], [1, L("Повторять, пока кнопка зажата", "Repeat while the button is held")], [3, L("Повторять до повторного нажатия", "Repeat until the button is pressed again")]].map(([t, txt]) =>
        h("label", { class: "check" }, h("input", { type: "radio", name: "mmode", checked: m.mode === t, onchange: () => { m.mode = t; mark(); renderView(); } }), h("span", {}, txt))),
      m.mode === 2 ? h("label", { class: "check" }, "N = ", h("input", { type: "number", min: 1, max: 1000, value: m.repeat || 1, style: "width:70px",
        onchange: (e) => { m.repeat = Math.max(1, Math.min(1000, +e.target.value | 0)); e.target.value = m.repeat; mark(); } })) : null),
    h("h3", {}, L("Действия", "Actions")),
    h("div", { class: "evtable" }, h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "#"), h("th", {}, L("Тип", "Type")), h("th", {}, L("Действие", "Action")), h("th", {}, L("Пауза перед", "Delay before")), h("th", {}))),
      h("tbody", {}, rows.length ? rows : h("tr", {}, h("td", { colspan: 5, class: "muted", style: "padding:16px" }, L("Пусто. Запишите действия или добавьте их ниже.", "Empty. Record actions or add them below.")))))),
    h("div", { class: "row small muted", style: "margin-top:6px" }, h("span", {}, m.events.length + L(" действий, ", " actions, ") + L("длительность ", "duration ") + (m.events.reduce((a, e) => a + (e.delay || 0), 0) / 1000).toFixed(2) + L(" с", " s")),
      h("span", { class: "grow" }), h("span", {}, evSel >= 0 ? L("новые действия вставляются после строки ", "new actions go after row ") + (evSel + 1) : L("новые действия добавляются в конец", "new actions are added at the end"))),
    ...warns.map((w) => h("p", { class: "small", style: "color:var(--warn)" }, "⚠ " + w)),

    h("h3", {}, L("Запись", "Recording")), pad,
    h("div", { class: "row", style: "margin-top:10px" },
      h("button", { class: recording ? "danger" : "primary", onclick: () => { if (recording) { stopRec(); renderView(); } else startRec(); } }, recording ? "■ " + L("Остановить", "Stop") : "● " + L("Записать", "Record")),
      h("label", { class: "check" }, h("input", { type: "checkbox", checked: pref("recDelays", true), onchange: (e) => setPref("recDelays", e.target.checked) }), h("span", {}, L("записывать реальные паузы", "record real delays"))),
      h("label", { class: "check" }, L("иначе ", "otherwise "), h("input", { type: "number", min: 0, max: MAX_DELAY, value: pref("recFixed", 30), style: "width:70px", onchange: (e) => setPref("recFixed", Math.max(0, Math.min(MAX_DELAY, +e.target.value | 0))) }), " " + ms)),

    h("h3", {}, L("Добавить вручную", "Add manually")),
    h("div", { class: "row" }, keySel,
      h("button", { class: "small", onclick: () => insert([{ type: "keydown", key: k(), delay: 30 }, { type: "keyup", key: k(), delay: 30 }]) }, L("Нажать и отпустить", "Press and release")),
      h("button", { class: "small", onclick: () => insert([{ type: "keydown", key: k(), delay: 30 }]) }, L("Зажать", "Press")),
      h("button", { class: "small", onclick: () => insert([{ type: "keyup", key: k(), delay: 30 }]) }, L("Отпустить", "Release"))),
    h("div", { class: "row" }, delay, " " + ms, h("button", { class: "small", onclick: () => insert([{ type: "delay", delay: Math.max(1, Math.min(MAX_DELAY, +delay.value | 0)) }]) }, L("Пауза", "Delay")),
      h("span", { style: "width:14px" }), "X", dx, "Y", dy, h("button", { class: "small", onclick: () => insert([{ type: "move", dx: +dx.value | 0, dy: +dy.value | 0, delay: 10 }]) }, L("Сдвинуть курсор", "Move the cursor")),
      h("span", { style: "width:14px" }), wheel, h("button", { class: "small", onclick: () => insert([{ type: "wheel", wheel: +wheel.value | 0, delay: 10 }]) }, L("Колесо", "Wheel"))),
    h("p", { class: "muted small" }, L("Сдвиг курсора — в пикселях относительно текущего положения (с учётом ускорения Windows). Колесо: плюс — вверх, минус — вниз.",
      "Cursor moves are in pixels relative to the current position (Windows pointer acceleration applies). Wheel: plus is up, minus is down.")),

    h("div", { class: "sep" }),
    uses.length ? h("p", { class: "small muted" }, L("Назначен: ", "Assigned to: ") + uses.map((u) => u.where + ", " + slotTitle(u.slot)).join("; ")) : h("p", { class: "small muted" }, L("Пока не назначен ни на одну кнопку.", "Not assigned to any button yet.")),
    h("div", { class: "row" },
      h("button", { class: "small", onclick: () => { const c = clone(m); c.id = newMacro().id; c.name = m.name + L(" (копия)", " (copy)"); D.macros.push(c); mSel = c.id; markDirty(); renderView(); } }, L("Копировать", "Duplicate")),
      h("button", { class: "small", disabled: !m.events.length, onclick: () => { m.events = []; evSel = -1; mark(); renderView(); } }, L("Очистить", "Clear")),
      h("span", { class: "grow" }),
      h("button", { class: "small danger", onclick: () => {
        const q = L("Удалить макрос «", "Delete macro “") + m.name + L("»?", "”?") + (uses.length ? "\n\n" + L("Он назначен на кнопки — они станут «не заданы»: ", "It is assigned to buttons — they become “not set”: ") + uses.map((u) => u.where + ", " + slotTitle(u.slot)).join("; ") : "");
        if (!confirm(q)) return;
        stopRec();
        for (const u of uses) delete u.map[u.slot];
        D.macros.splice(D.macros.indexOf(m), 1);
        mSel = null; evSel = -1; markDirty(); renderView();
      } }, L("Удалить макрос", "Delete macro"))),
    h("p", { class: "muted small" }, L("Изменения макросов сохраняются общей кнопкой «Сохранить» вверху.", "Macro changes are saved with the Save button at the top.")));
}

// ---------------------------------------------------------------------------
// DPI & lights tab

function stagesEditor(set, live) {
  const st = S.status;
  const box = h("div", { class: "steps" });
  set.stages.forEach((val, i) => {
    const isCur = live && st.stagesFrom === "global" && st.stage === i;
    box.append(h("div", { class: "stage" + (isCur ? " cur" : "") },
      h("span", { class: "lv" }, i + 1),
      h("input", { type: "number", min: 100, max: 5600, step: 100, value: val, onchange: (e) => { set.stages[i] = Math.round((+e.target.value || 100) / 100) * 100; markDirty(); renderView(); } }),
      h("label", { class: "check" }, h("input", { type: "radio", name: "start" + (live ? "g" : Math.random()), checked: set.start === i, onchange: () => { set.start = i; markDirty(); } }),
        h("span", { class: "small muted" }, L("при включении", "initial"))),
      h("div", { class: "row" },
        live ? h("button", { class: "small", disabled: !st.mouse || dirty, title: dirty ? L("сначала сохраните", "save first") : "", onclick: () => api("stage", { index: i }).then((r) => { S = r; renderHeader(); renderView(); }) }, L("Выбрать", "Select")) : null,
        set.stages.length > 1 ? h("button", { class: "small danger", onclick: () => { set.stages.splice(i, 1); if (set.start >= set.stages.length) set.start = 0; markDirty(); renderView(); } }, "×") : null)));
  });
  if (set.stages.length < 8) box.append(h("div", {}, h("button", { class: "small", onclick: () => { set.stages.push(Math.min(5600, (set.stages[set.stages.length - 1] || 800) + 400)); markDirty(); renderView(); } }, "+ " + L("ступень", "stage"))));
  return box;
}

function renderMouse(v) {
  const s = D.settings;
  const st = S.status;
  v.append(h("div", { class: "card" }, h("h2", {}, L("Ступени DPI", "DPI stages")),
    h("p", { class: "muted small" }, L("Мышь сама не хранит ступени: их переключает приложение кнопками с действием «DPI». 100–5600 с шагом 100. У профиля могут быть свои ступени.",
      "The mouse has no stages of its own: the app switches them with buttons set to a DPI action. 100–5600 in steps of 100. A profile can have its own stages.")),
    stagesEditor(s.dpi, true),
    st.stagesFrom !== "global" ? h("p", { class: "muted small" }, L("Сейчас действуют ступени профиля «", "The stages of profile “") + st.activeName + L("».", "” are in use now.")) : null));
  const poll = h("div", { class: "seg" }, ...[125, 500, 1000].map((hz) => h("button", { class: s.pollRate === hz ? "on" : "", onclick: () => { s.pollRate = hz; markDirty(); renderView(); } }, hz + " " + L("Гц", "Hz"))));
  const light = (k, text) => h("label", { class: "check" }, h("input", { type: "checkbox", checked: s[k], onchange: (e) => { s[k] = e.target.checked; markDirty(); } }), h("span", {}, text));
  v.append(h("div", { class: "grid2" },
    h("div", { class: "card" }, h("h2", {}, L("Частота опроса", "Polling rate")), poll),
    h("div", { class: "card" }, h("h2", {}, L("Подсветка", "Lights")),
      light("lightWheel", L("Колесо", "Scroll wheel")), light("lightLogo", L("Логотип", "Logo")), light("lightPad", L("Боковая панель", "Side keypad")),
      h("p", { class: "muted small" }, L("У этой модели подсветка только включается и выключается, цвета нет.", "This model's lights only switch on and off; there is no colour.")))));
}

// ---------------------------------------------------------------------------
// Chatter tab

function renderChatter(v) {
  const c = D.settings.chatter;
  const st = S.status;
  const num = (k, text, hint) => h("div", { class: "field" }, h("span", {}, text),
    h("input", { type: "number", min: 0, max: 1000, value: c[k], onchange: (e) => { c[k] = Math.max(0, Math.round(+e.target.value || 0)); markDirty(); } }), h("span", { class: "small muted" }, hint));
  v.append(h("div", { class: "card" }, h("h2", {}, L("Фильтр дребезга левой кнопки", "Left-button chatter filter")),
    h("p", { class: "muted small" }, L("Изношенная кнопка превращает одно нажатие в два-три. Фильтр ненадолго придерживает отпускание кнопки: если сразу за ним приходит новое нажатие, это дребезг, и программа видит одно нажатие. Перетаскивание не обрывается.",
      "A worn switch turns one press into two or three. The filter holds back each release briefly: if a new press follows right away, it is a bounce and programs see one press. Drags do not break.")),
    h("label", { class: "check" }, h("input", { type: "checkbox", checked: c.enabled, onchange: (e) => { c.enabled = e.target.checked; markDirty(); } }), h("span", {}, L("Включить фильтр", "Enable the filter"))),
    h("div", { class: "row" }, num("t", L("Пауза T, мс", "Gap T, ms"), L("короче — дребезг", "shorter = bounce")),
      num("h", L("Нажатие H, мс", "Press H, ms"), L("короче — обрывок", "shorter = fragment")),
      num("g", L("Пауза G, мс", "Gap G, ms"), L("после обрывка", "after a fragment")),
      num("d", L("Перетаскивание D, мс", "Drag D, ms"), L("пропадание контакта при движении", "contact drop while moving"))),
    h("p", { class: "muted small" }, L("T, H, G подбираются калибровкой по кликам. D действует, только когда курсор двигается с нажатой кнопкой: отпускание в конце перетаскивания приходит на D позже, зато случайный обрыв контакта короче D объект не роняет. Если перетаскивание всё ещё рвётся — увеличьте D.",
      "T, H and G come from the click calibration. D applies only while the cursor moves with the button held: the release at the end of a drag arrives D later, but a contact drop shorter than D does not drop the object. If drags still break, increase D.")),
    h("p", { class: "muted small" }, c.calibrated && !c.calibrated.startsWith("0001") ? L("Последняя калибровка: ", "Last calibration: ") + new Date(c.calibrated).toLocaleString() + (c.score ? " — " + c.score : "") : L("Калибровка ещё не проводилась.", "Not calibrated yet.")),
    st.presses ? h("p", { class: "muted small" }, L("С запуска: нажатий ", "Since start: presses ") + st.presses + L(", убрано дребезга ", ", bounces removed ") + st.merged) : null));
  v.append(renderWizard());
}

// The wizard's own buttons are clicked with the chattering left button, so
// every action is guarded: one action at a time, none within 600 ms of the
// previous one. The step is derived from what the app has recorded, not
// counted locally, so a doubled click cannot skip a step.
async function wizAct(fn) {
  const now = Date.now();
  if (calib.busy || now - (calib.last || 0) < 600) return;
  calib.busy = true;
  try { await fn(); } catch (e) { toast(e.message, true); }
  calib.busy = false;
  calib.last = Date.now();
  renderView();
}

function wizStep() {
  const st = S.status;
  if (st.calibPhase === "single" || !st.calibSingle) return 0;
  if (st.calibPhase === "double" || !st.calibDouble) return 1;
  return 2;
}

function renderWizard() {
  const st = S.status;
  const step = wizStep();
  const card = h("div", { class: "card" }, h("h2", {}, L("Калибровка по вашим кликам", "Calibrate from your clicks")));
  const steps = [
    [L("Шаг 1", "Step 1"), L("15 одиночных кликов", "15 single clicks")],
    [L("Шаг 2", "Step 2"), L("8 двойных кликов", "8 double clicks")],
    [L("Шаг 3", "Step 3"), L("Результат", "Result")],
  ];
  card.append(h("div", { class: "wiz" }, ...steps.map(([a, b], i) => h("div", { class: step === i ? "on" : step > i ? "done" : "" }, h("b", {}, a), b))));
  const reset = h("button", { onclick: () => wizAct(async () => { S = await api("calib/reset", {}); calib.result = null; }) }, L("Сбросить калибровку", "Reset the calibration"));
  if (step < 2) {
    const phase = step === 0 ? "single" : "double";
    const rec = st.calibPhase === phase;
    const target = step === 0 ? 15 : 8;
    card.append(h("p", {}, step === 0
      ? L("Нажмите «Начать», затем кликайте левой кнопкой в рамке ниже по одному разу, медленно — примерно раз в секунду. 15 кликов. Потом «Готово».", "Press Start, then click once at a time inside the box below, slowly — about once a second. 15 clicks. Then Done.")
      : L("Теперь сделайте 8 двойных кликов в рамке, так, как вы делаете обычно, с паузой около секунды между ними. Потом «Готово».", "Now double-click 8 times inside the box, the way you normally do, pausing about a second between them. Then Done.")));
    card.append(h("div", { class: "clickpad" + (rec ? " rec" : "") },
      rec ? h("span", { class: "big", id: "calibCount" }, st.calibCount) : h("span", {}, L("здесь", "here")),
      h("span", { class: "small" }, rec ? L("нажатий записано (с дребезгом их больше, чем кликов)", "presses recorded (with chatter there are more than clicks)") : "")));
    card.append(h("div", { class: "row" },
      rec ? h("button", { class: "primary", onclick: () => wizAct(async () => {
              S = await api("calib/stop", {});
              const n = phase === "single" ? S.status.calibSingle : S.status.calibDouble;
              if (n < target) toast(L("Записано мало нажатий: ", "Too few presses recorded: ") + n + L(". Пройдите шаг ещё раз.", ". Please repeat the step."), true);
              if (wizStep() === 2) runFit();
            }) }, L("Готово", "Done"))
        : h("button", { class: "primary", onclick: () => wizAct(async () => { S = await api("calib/start", { phase }); }) }, L("Начать", "Start")),
      rec ? h("button", { onclick: () => wizAct(async () => { S = await api("calib/reset", { phase }); }) }, L("Отмена", "Cancel")) : null,
      step > 0 && !rec ? reset : null,
      h("span", { class: "muted small" }, L("цель: ", "target: ") + target + (step === 0 ? L(" кликов", " clicks") : L(" двойных", " doubles")))));
  } else {
    if (!calib.result && !calib.busy) runFit();
    const r = calib.result;
    if (!r) card.append(h("p", { class: "muted" }, L("Считаю…", "Computing…")));
    else if (r.error) card.append(h("p", {}, r.error));
    else {
      card.append(h("div", { class: "kv" },
        h("span", {}, L("Без фильтра", "Without the filter")), h("span", {}, L("одиночных верно ", "singles right ") + r.rawSinglesOk + "/" + r.singles + L(", двойных ", ", doubles ") + r.rawDoublesOk + "/" + r.doubles),
        h("span", {}, L("С подобранным фильтром", "With the fitted filter")), h("span", {}, h("b", {}, L("одиночных ", "singles ") + r.singlesOk + "/" + r.singles + L(", двойных ", ", doubles ") + r.doublesOk + "/" + r.doubles)),
        h("span", {}, L("Дребезг в шаге 1", "Chatter in step 1")), h("span", {}, r.chatterSeen + L(" раз, самый длинный ", " times, longest ") + r.maxChatterMs.toFixed(1) + L(" мс", " ms")),
        h("span", {}, L("Параметры", "Parameters")), h("span", {}, "T = " + r.T + L(" мс, H = ", " ms, H = ") + r.H + L(" мс, G = ", " ms, G = ") + r.G + L(" мс", " ms"))));
      if (r.chatterSeen === 0) card.append(h("p", { class: "muted small" }, L("В шаге 1 дребезга не было: фильтр сейчас не нужен.", "No chatter in step 1: the filter is not needed right now.")));
      if (r.singles !== 15 || r.doubles !== 8) card.append(h("p", { class: "muted small" }, L("Распознано одиночных: ", "Recognised singles: ") + r.singles + L(", двойных: ", ", doubles: ") + r.doubles + L(". Если это не совпадает с тем, что вы делали, сбросьте и пройдите шаги ещё раз с паузами подлиннее.", ". If that is not what you did, reset and repeat the steps with longer pauses.")));
    }
    card.append(h("div", { class: "row" },
      r && !r.error ? h("button", { class: "primary", onclick: () => wizAct(async () => {
        Object.assign(D.settings.chatter, { enabled: true, t: r.T, h: r.H, g: r.G, calibrated: new Date().toISOString(), score: r.score });
        await save();
      }) }, L("Применить и сохранить", "Apply and save")) : null,
      reset));
  }
  return card;
}

async function runFit() {
  calib.result = null;
  calib.busy = true;
  try { calib.result = await api("calib/result", {}); } catch (e) { calib.result = { error: e.message }; }
  calib.busy = false;
  renderView();
}

// on-screen popups: when to show them and how they look (as in SM809 Control)
const POPUP_DEFAULT = { position: "bottom-center", offsetX: 0, offsetY: 120, width: 240, height: 120, cornerRadius: 18, background: "#1e1f22", opacity: 225,
  textColor: "#ffffff", captionColor: "#9aa0a6", accentColor: "#44d62c", dotColor: "#4a4d52", font: "Segoe UI", fontSize: 40, captionSize: 13, bold: true, durationMs: 1200, fadeMs: 350 };

function popupCard() {
  const s = D.settings, ps = s.popup;
  const chk = (obj, k, text, hint) => h("label", { class: "check" }, h("input", { type: "checkbox", checked: !!obj[k], onchange: (e) => { obj[k] = e.target.checked; markDirty(); } }),
    h("span", {}, text, hint ? h("small", {}, hint) : null));
  const num = (k, text, min, max) => h("div", { class: "field" }, h("span", {}, text),
    h("input", { type: "number", min, max, value: ps[k], onchange: (e) => { ps[k] = Math.max(min, Math.min(max, Math.round(+e.target.value || 0))); e.target.value = ps[k]; markDirty(); } }));
  const col = (k, text) => h("div", { class: "field" }, h("span", {}, text),
    h("input", { type: "color", value: ps[k], oninput: (e) => { ps[k] = e.target.value; markDirty(); } }));
  const POS = [["bottom-center", L("снизу по центру", "bottom centre")], ["top-center", L("сверху по центру", "top centre")], ["center", L("по центру", "centre")],
    ["top-left", L("слева сверху", "top left")], ["top-right", L("справа сверху", "top right")], ["bottom-left", L("слева снизу", "bottom left")],
    ["bottom-right", L("справа снизу", "bottom right")], ["cursor", L("у курсора", "at the cursor")]];
  const test = (kind) => api("osd-test", { kind, style: ps }).catch((e) => toast(e.message, true));
  return h("div", { class: "card" }, h("h2", {}, L("Подсказки на экране", "On-screen popups")),
    chk(s, "osdDpi", L("При смене DPI", "On DPI change")),
    chk(s, "osdProfile", L("При смене профиля", "On profile change"), L("для каждого профиля включается отдельно во вкладке «Профили»", "turned on per profile on the Profiles tab")),
    h("h3", {}, L("Положение и размер", "Position and size")),
    h("div", { class: "row" },
      h("div", { class: "field" }, h("span", {}, L("Положение", "Position")),
        h("select", { onchange: (e) => { ps.position = e.target.value; markDirty(); } }, ...POS.map(([v, t]) => h("option", { value: v, selected: ps.position === v }, t)))),
      num("offsetX", L("Смещение X, px", "Offset X, px"), -4000, 4000), num("offsetY", L("Смещение Y, px", "Offset Y, px"), -4000, 4000)),
    h("div", { class: "row" }, num("width", L("Ширина", "Width"), 60, 2000), num("height", L("Высота", "Height"), 30, 2000), num("cornerRadius", L("Скругление", "Corner radius"), 0, 500)),
    h("p", { class: "muted small" }, L("Смещение отсчитывается от выбранного края экрана внутрь (у курсора — от курсора). Размеры — при масштабе 100 %, на мониторах с другим масштабом подстраиваются.",
      "The offset is measured inwards from the chosen screen edge (from the cursor for “at the cursor”). Sizes are at 100 % scaling and adapt to other monitor scaling.")),
    h("h3", {}, L("Внешний вид", "Appearance")),
    h("div", { class: "row" }, col("background", L("Фон", "Background")),
      h("div", { class: "field" }, h("span", {}, L("Непрозрачность", "Opacity")),
        h("input", { type: "range", min: 0, max: 255, value: ps.opacity, style: "width:130px", oninput: (e) => { ps.opacity = +e.target.value; markDirty(); } })),
      col("textColor", L("Текст", "Text")), col("captionColor", L("Подпись", "Caption")), col("accentColor", L("Акцент", "Accent")), col("dotColor", L("Точки", "Dots"))),
    h("div", { class: "row" },
      h("div", { class: "field" }, h("span", {}, L("Шрифт", "Font")), h("input", { type: "text", value: ps.font, style: "width:150px", oninput: (e) => { ps.font = e.target.value; markDirty(); } })),
      num("fontSize", L("Размер", "Size"), 6, 300), num("captionSize", L("Подпись", "Caption"), 6, 200),
      h("div", { class: "field" }, h("span", {}, " "), chk(ps, "bold", L("Жирный", "Bold")))),
    h("div", { class: "row" }, num("durationMs", L("Показывать, мс", "Show for, ms"), 100, 60000), num("fadeMs", L("Затухание, мс", "Fade, ms"), 0, 10000)),
    h("div", { class: "row", style: "margin-top:14px" },
      h("button", { onclick: () => test("dpi") }, L("Показать окно DPI", "Preview the DPI popup")),
      h("button", { onclick: () => test("profile") }, L("Показать окно профиля", "Preview the profile popup")),
      h("button", { class: "small", onclick: () => { s.popup = clone(POPUP_DEFAULT); markDirty(); renderView(); } }, L("Стиль по умолчанию", "Default style"))),
    h("p", { class: "muted small" }, L("Пример показывается с текущими, ещё не сохранёнными настройками.", "The preview uses the current settings, even unsaved ones.")));
}

// ---------------------------------------------------------------------------
// Settings tab

function renderSettings(v) {
  const s = D.settings;
  const st = S.status;
  v.append(h("div", { class: "card" }, h("h2", {}, L("Общие", "General")),
    h("div", { class: "row" }, h("span", {}, L("Язык", "Language")),
      h("select", { onchange: (e) => { s.language = e.target.value; markDirty(); } }, h("option", { value: "ru", selected: s.language === "ru" }, "Русский"), h("option", { value: "en", selected: s.language === "en" }, "English"))),
    h("label", { class: "check" }, h("input", { type: "checkbox", checked: st.autostart, onchange: (e) => api("autostart", { on: e.target.checked }).then((r) => { S = r; }) }), h("span", {}, L("Запускать вместе с Windows", "Start with Windows"))),
    h("label", { class: "check" }, h("input", { type: "checkbox", checked: s.keyRepeat, onchange: (e) => { s.keyRepeat = e.target.checked; markDirty(); } }),
      h("span", {}, L("Автоповтор клавиш при удержании кнопки", "Repeat keys while a button is held"), h("small", {}, L("Как на обычной клавиатуре: через 0,5 с, 30 раз в секунду. Только для одиночных клавиш.", "Like a keyboard: after 0.5 s, 30 times a second. Single keys only.")))),
    h("label", { class: "check" }, h("input", { type: "checkbox", checked: s.debug, onchange: (e) => { s.debug = e.target.checked; markDirty(); } }), h("span", {}, L("Журнал для отладки", "Debug log"))),
    h("div", { class: "row" }, h("button", { class: "small", onclick: () => api("open", { what: "data" }) }, L("Открыть папку с настройками", "Open the settings folder")))));
  v.append(h("div", { class: "card" }, h("h2", {}, L("Активное окно", "Active window")),
    h("div", { class: "row" }, h("div", { class: "field" }, h("span", {}, L("Проверять активное окно раз в, мс", "Check the active window every, ms")),
      h("input", { type: "number", min: 250, max: 30000, step: 250, value: s.fgCheckMs || 2000,
        onchange: (e) => { s.fgCheckMs = Math.max(250, Math.min(30000, Math.round(+e.target.value || 2000))); e.target.value = s.fgCheckMs; markDirty(); } })),
      h("span", { class: "muted small" }, L("сейчас: ", "now: ") + (st.foreground || "—"))),
    h("p", { class: "muted small" }, L("Обычно профиль переключается сразу — Windows сообщает о смене активного окна. Проверка по таймеру ловит окна, которые об этом не сообщают (некоторые игры, полноэкранные и UWP-приложения). Меньше — быстрее реакция в таких окнах, больше — меньше нагрузка. 250–30000 мс, по умолчанию 2000.",
      "Usually the profile switches at once — Windows reports the change of the active window. The timer check catches windows that do not report it (some games, fullscreen and UWP apps). Lower reacts faster in such windows, higher means less load. 250–30000 ms, 2000 by default."))));
  v.append(popupCard());

  const pad = h("div", { class: "card" }, h("h2", {}, L("Боковая панель", "Side keypad")));
  const status = { ready: L("работает через WinUSB — всё в порядке", "running through WinUSB — all good"), keyboard: L("работает как обычная клавиатура (драйвер ", "works as a plain keyboard (driver ") + (st.padService || "?") + ")",
    absent: L("не найдена — мышь не подключена?", "not found — is the mouse connected?"), error: L("ошибка: ", "error: ") + (st.padErr || "") }[st.pad];
  pad.append(h("p", {}, h("b", {}, L("Состояние: ", "Status: ")), status));
  pad.append(h("p", { class: "muted small" }, L("Windows считает боковую панель отдельной клавиатурой, и по её нажатиям нельзя понять, с какой клавиатуры пришла цифра. Поэтому панель переводится на драйвер Microsoft WinUSB: Windows перестаёт видеть её как клавиатуру, и только Naga Control решает, что отправить. Мышь при этом не затрагивается.",
    "Windows sees the side keypad as a separate keyboard, and its key presses do not say which keyboard sent a digit. So the keypad is switched to Microsoft's WinUSB driver: Windows no longer treats it as a keyboard and only Naga Control decides what to send. The mouse itself is not affected.")));
  pad.append(h("ol", { class: "steps-list" },
    h("li", {}, L("Скачайте и запустите Zadig.", "Download and run Zadig."), " ", h("a", { href: "#", onclick: (e) => { e.preventDefault(); api("open", { what: "zadig" }); } }, "zadig.akeo.ie")),
    h("li", {}, L("Меню Options → List All Devices.", "Options menu → List All Devices.")),
    h("li", {}, L("Выберите ", "Pick "), h("b", {}, "Razer Naga (Interface 1)"), L(" — именно Interface 1. Interface 0 — это сама мышь.", " — Interface 1 exactly. Interface 0 is the mouse itself.")),
    h("li", {}, L("Справа должно быть WinUSB. Нажмите Replace Driver.", "WinUSB must be shown on the right. Press Replace Driver.")),
    h("li", {}, L("Готово: состояние выше сменится на «работает через WinUSB».", "Done: the status above changes to “running through WinUSB”."))));
  pad.append(h("p", { class: "muted small" }, L("Без запущенного Naga Control панель после этого молчит. Вернуть как было: Диспетчер устройств → Razer Naga (Interface 1) → Удалить устройство с галочкой «Удалить драйвер» → переподключить мышь.",
    "Without Naga Control running the keypad stays silent afterwards. To undo: Device Manager → Razer Naga (Interface 1) → Uninstall device with “Delete the driver” ticked → reconnect the mouse.")),
    h("div", { class: "row" }, h("button", { class: "small", onclick: () => api("open", { what: "devmgr" }) }, L("Диспетчер устройств", "Device Manager"))));
  v.append(pad);
}

// ---------------------------------------------------------------------------

function renderView() {
  const v = document.getElementById("view");
  const y = v.scrollTop;
  v.innerHTML = "";
  ({ buttons: renderButtons, profiles: renderProfiles, macros: renderMacros, mouse: renderMouse, chatter: renderChatter, settings: renderSettings })[tab](v);
  v.scrollTop = y;
}

function renderAll() {
  document.documentElement.lang = LANG;
  renderHeader(); renderBanner(); renderTabs(); renderEditbar(); renderView();
}

function onStatus(st) {
  const prev = S.status;
  S.status = st;
  renderHeader();
  const bannerKey = (s) => [s.mouse, s.pad, s.hook, s.devErr, s.padErr].join("|");
  if (bannerKey(prev) !== bannerKey(st)) renderBanner();
  // live parts of the current view
  if (tab === "chatter" && st.calibPhase) {
    const c = document.getElementById("calibCount");
    if (c) c.textContent = st.calibCount; else renderView();
  } else if ((tab === "settings" && prev.pad !== st.pad) || (tab === "mouse" && (prev.stage !== st.stage || prev.stagesFrom !== st.stagesFrom)) || (tab === "profiles" && prev.active !== st.active)) {
    if (!document.activeElement || !["INPUT", "SELECT"].includes(document.activeElement.tagName)) renderView();
  }
}

async function init() {
  try {
    S = await api("state");
  } catch (e) {
    document.body.textContent = L("Нет связи с Naga Control: ", "No connection to Naga Control: ") + e.message;
    return;
  }
  LANG = S.lang || "ru";
  D = normalize(clone(S.data));
  renderAll();
  const es = new EventSource("/api/events?t=" + encodeURIComponent(TOKEN));
  es.onmessage = (m) => { try { onStatus(JSON.parse(m.data)); } catch (e) { /* ignore */ } };
  window.addEventListener("beforeunload", (e) => { if (dirty) { e.preventDefault(); e.returnValue = ""; } });
}
init();
