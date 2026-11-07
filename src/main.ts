import { Api, events, type Event, type Library, type SearchResult, type Settings, type State, type State as RunState, type TrackState } from "./mygo";
import { isCallError } from "mygo-runtime";
import "./style.css";

const $ = <T extends HTMLElement>(sel: string) => document.querySelector<T>(sel)!;

/** The poster stripes, in order. */
const STRIPES: string[] = ["#e8321e", "#1d3fd6", "#f7e81a", "#ff3e9c", "#5fdcb4", "#ff6b12", "#2e9e4f"];

let settings: Settings;
let run: RunState | null = null;
let picking: number[] = [];
let lastLog = "";
let logLines: string[] = [];
let toastTimer = 0;

// --------------------------------------------------------------- helpers

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls = "", text = ""): HTMLElementTagNameMap[K] {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text) n.textContent = text;
  return n;
}

function toast(msg: string) {
  const t = $("#toast");
  t.textContent = msg;
  t.hidden = false;
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => (t.hidden = true), 2600);
}

function fail(err: unknown) {
  const msg = isCallError(err) ? err.message : String(err);
  toast(msg);
}

/** "1.3 MB/s", or an em dash when nothing is moving. */
function fmtSpeed(b: number): string {
  if (!b || b <= 0) return "—";
  return b >= 1_000_000 ? `${(b / 1_000_000).toFixed(1)} MB/s` : `${Math.round(b / 1000)} KB/s`;
}

function fmtSize(b: number): string {
  if (!b) return "";
  return b >= 1_000_000 ? `${(b / 1_000_000).toFixed(1)} MB` : `${Math.round(b / 1000)} KB`;
}

function clock(secs: number): string {
  const s = Math.max(0, Math.floor(secs));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, "0")}`;
}

/** The stripe colour of a row, by index. */
function stripeAt(i: number): string {
  return STRIPES[i % STRIPES.length] ?? STRIPES[0]!;
}

function shortDir(dir: string): string {
  const home = dir.replace(/^\/home\/[^/]+/, "~");
  return home.length > 34 ? "…" + home.slice(-33) : home;
}

function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many;
}

function kindLabel(kind: string): string {
  switch (kind) {
    case "playlist":
      return "PLAYLIST";
    case "album":
      return "ALBUM";
    case "track":
      return "TRACK";
    case "library":
      return "LIBRARY";
    default:
      return "COLLECTION";
  }
}

function isSpotifyLink(url: string): boolean {
  return /^https?:\/\/open\.spotify\.com\/(playlist|album|track|artist)\//.test(url.trim());
}

// ------------------------------------------------------------ graph paper

function drawGrid() {
  const g = $("#grid");
  g.innerHTML = "";
  const step = 44;
  const w = window.innerWidth;
  const h = window.innerHeight;
  for (let x = step; x < w; x += step) {
    const line = el("i");
    line.style.left = `${x}px`;
    line.style.top = "0px";
    line.style.width = "1px";
    line.style.height = `${h}px`;
    g.append(line);
  }
  for (let y = step; y < h; y += step) {
    const line = el("i");
    line.style.left = "0px";
    line.style.top = `${y}px`;
    line.style.width = `${w}px`;
    line.style.height = "1px";
    g.append(line);
  }
}

// ------------------------------------------------------------------ screens

function show(which: "idle" | "working" | "poster" | "history") {
  for (const name of ["idle", "working", "poster", "history"] as const) {
    $(`#s-${name}`).hidden = name !== which;
  }
}

function screen(): "idle" | "working" | "poster" | "history" {
  if (run && (run.phase === "spotify" || run.phase === "soulseek" || run.phase === "youtube" || run.phase === "convert" || run.phase === "choosing" || run.phase === "failed")) {
    return "working";
  }
  if (run && run.phase === "done" && run.tracks.length > 0) return "poster";
  if (run && run.phase === "stopped" && run.tracks.length > 0) return "poster";
  return blockedByHistory ? "history" : "idle";
}

let blockedByHistory = false;

// -------------------------------------------------------------------- idle

function renderIdle() {
  const link = $<HTMLInputElement>("#link");
  const text = link.value.trim();
  const ok = isSpotifyLink(text);
  const typed = text.length > 0;

  const title = $("#idle-title");
  if (!title.dataset.done) {
    title.innerHTML = "";
    title.append(el("span", "", "Paste a"), el("span", "", "Spotify link."));
    title.dataset.done = "1";
  }
  $("#underline").className = "underline" + (ok ? " ok" : typed ? " typed" : "");
  $("#go").className = "go" + (ok ? " ok" : "");
  $<HTMLButtonElement>("#go").disabled = !ok;

  const hint = $("#hint");
  if (typed && !ok) {
    hint.textContent = "That is not a Spotify link. Playlists, albums and tracks work.";
    hint.className = "hint warn";
  } else if (ok) {
    hint.textContent = "Press Enter ↵ to download";
    hint.className = "hint";
  } else {
    const paste = navigator.platform.toLowerCase().includes("mac") ? "⌘V" : "Ctrl+V";
    hint.textContent = `Paste with ${paste} anywhere · playlist, album or track`;
    hint.className = "hint";
  }

  const noAccount = !settings.wantSoulseek || !settings.soulseekUser;
  const note = $<HTMLButtonElement>("#no-account");
  note.hidden = !noAccount;
  $("#no-account-text").textContent =
    "Not signed in to Soulseek · downloads come from YouTube Music (slower) · sign in →";
  $("#account-ping").hidden = !noAccount;

  $("#where").textContent = `${shortDir(settings.outputDir)} · MP3 ${settings.bitrate}`;
  $("#open-history").textContent = "History";
  $("#open-settings").innerHTML = "";
  const ping = el("span", "ping");
  ping.append(el("i"));
  if (noAccount) $("#open-settings").append(ping);
  $("#open-settings").append(document.createTextNode("Settings"));
}

async function start() {
  const text = $<HTMLInputElement>("#link").value.trim();
  if (!isSpotifyLink(text)) {
    toast("That is not a Spotify link");
    return;
  }
  blockedByHistory = false;
  run = null;
  logLines = [];
  try {
    await Api.startInput(text, settings.wantSoulseek && settings.soulseekUser ? false : false);
    toast("Reading Spotify…");
    $<HTMLInputElement>("#link").value = "";
    renderIdle();
  } catch (err) {
    fail(err);
  }
}

// ---------------------------------------------------------------- working

const STATUS: Record<string, string> = {
  spotify: "Reading Spotify",
  soulseek: "Searching Soulseek",
  youtube: "Searching YouTube Music",
  convert: "Converting to mp3",
  done: "Finishing",
  choosing: "Pick the tracks",
  failed: "Failed",
  idle: "Connecting",
  stopped: "Stopped",
};

function statusText(s: RunState): string {
  if (s.phase === "failed") return "Failed";
  if (s.phase === "choosing") return "Pick the tracks";
  if (s.stopped) return "Stopping · saving the finished songs";
  if (s.phase === "convert") return "Converting to mp3";
  const downloading = s.tracks.some((t) => t.state === "downloading" || t.state === "yt_download");
  if (s.phase === "youtube") return downloading ? "Downloading from YouTube Music" : "Searching YouTube Music";
  if (s.phase === "soulseek") return downloading ? "Downloading from Soulseek" : "Searching Soulseek";
  return STATUS[s.phase] ?? "Connecting";
}

function rowStateClass(t: TrackState): string {
  switch (t.state) {
    case "searching":
      return "blinking";
    case "downloading":
      return "downloading";
    case "failed":
    case "yt_search":
    case "yt_download":
      return "yt blinking";
    case "done":
      return "";
    case "missing":
      return "missing";
    default:
      return "pending";
  }
}

/** What the little chip at the right of a row says. */
function chipFor(t: TrackState): string {
  switch (t.state) {
    case "searching":
      return "…";
    case "downloading": {
      const p = t.total > 0 ? t.bytes / t.total : 0;
      return `${Math.round(p * 100)}%`;
    }
    case "done":
      return t.source === "youtube" ? "YT" : "OK";
    case "failed":
      return "TRYING YT…";
    case "yt_search":
    case "yt_download":
      return "→ YT";
    case "missing":
      return "MISSING";
    default:
      return "";
  }
}

function renderWorking(s: RunState) {
  const n = s.tracks.length;
  $("#status").textContent = statusText(s);
  $("#status-dot").className = "dot" + (s.phase === "failed" ? " failed" : "");
  $("#running-link").textContent = s.link ?? "";

  const coll = n === 0 ? "…" : `${kindLabel(s.kind)}: ${(s.name ?? "").toUpperCase()} · ${n} ${plural(n, "track", "tracks")}`;
  $("#stat-coll").textContent = coll;
  $("#stat-progress").textContent = `${s.finished ?? 0}/${n}`;
  $("#stat-speed").textContent = fmtSpeed(s.speed ?? 0);

  $("#working-title").textContent = statusText(s);

  const pct = Math.round((s.progress ?? 0) * 100);
  $("#bar-fill").style.width = `${pct}%`;
  $("#pct").textContent = `${pct}%`;
  $("#bar-dir").textContent = `⟵ ${shortDir(s.outDir || settings.outputDir).toUpperCase()}`;
  const peers: string[] = s.peers ?? [];
  const peerLabel =
    s.phase === "convert"
      ? `CONVERTING ${s.convertDone ?? 0}/${s.convertTotal ?? 0}`
      : peers.length === 0
        ? "WAITING FOR PEERS"
        : peers.length === 1
          ? `FROM ${(peers[0] ?? "").toUpperCase()} ⟶`
          : `FROM ${(peers[0] ?? "").toUpperCase()} +${peers.length - 1} PEERS ⟶`;
  $("#bar-peer").textContent = peerLabel;

  const left = s.tracks.filter((t) => t.state !== "done" && t.state !== "missing" && t.state !== "skipped").length;
  $("#bar-count").textContent = `${s.done ?? 0} DONE · ${left} LEFT`;

  renderTracks(s);
  renderBelow(s);
  renderFooter(s);
}

function renderTracks(s: RunState) {
  const grid = $("#track-grid");
  grid.innerHTML = "";
  const n = s.tracks.length;
  if (n === 0) {
    grid.append(el("div", "bar-label blinking", "Reading the track list…"));
    return;
  }
  // Rows of cells, left to right: about one cell per 250px.
  const width = $("#track-grid").clientWidth || 900;
  const cols = Math.min(6, Math.max(1, Math.floor(width / 250)));
  for (let r = 0; r * cols < n; r++) {
    const row = el("div", "track-row");
    for (let i = r * cols; i < Math.min(n, (r + 1) * cols); i++) {
      const t = s.tracks[i];
      if (t) row.append(trackCell(t, i));
    }
    grid.append(row);
  }
}

function trackCell(t: TrackState, i: number): HTMLElement {
  const cell: HTMLDivElement = el("div", `track-cell ${rowStateClass(t)}`);
  if (t.state === "done" && t.source !== "youtube") {
    cell.style.background = stripeAt(i);
  }
  // A red fill that grows behind the title as bytes arrive.
  const fill = el("div", "fill");
  const p = t.state === "downloading" && t.total > 0 ? t.bytes / t.total : 0;
  fill.style.width = `${p * 100}%`;
  cell.append(fill, el("span", "num", `${String(i + 1).padStart(2, " ")}.`), el("span", "title", t.title), el("span", "chip", chipFor(t)));
  cell.title = `${t.artist} — ${t.title}`;
  return cell;
}

/** Banners under the bar: the account note and the "pick the tracks" list. */
function renderBelow(s: RunState) {
  const below = $("#below");
  // Keep the grid element, drop old banners.
  for (const b of Array.from(below.querySelectorAll(".banner"))) b.remove();
  const grid = $("#track-grid");

  if (s.phase === "failed" && s.error) {
    const b = el("div", "banner");
    b.style.cssText = "display:flex;flex-direction:column;gap:14px;padding-top:10px";
    const msg = el("p", "login-error", s.error);
    const again = el("button", "button dark", "TRY AGAIN");
    again.addEventListener("click", () => void start());
    const fresh = el("button", "button", "NEW DOWNLOAD");
    fresh.addEventListener("click", () => {
      run = null;
      show("idle");
    });
    const row = el("div", "row gap-10");
    row.append(again, fresh);
    b.append(msg, row);
    below.prepend(b);
    return;
  }

  // The picker: the run waits to know which tracks to fetch.
  if (s.phase === "choosing") {
    const b = el("div", "banner ask");
    b.style.cssText = "display:flex;align-items:center;gap:12px;width:100%;height:46px;padding:0 14px;background:var(--yellow);border:2px solid var(--ink)";
    const n = s.tracks.length;
    const msg = el("span", "", `${n} ${plural(n, "track", "tracks")} found · download them all, or pick below`);
    msg.style.cssText = "flex:1;min-width:0;font-size:13px;font-weight:600;text-transform:uppercase;overflow:hidden;text-overflow:ellipsis;white-space:nowrap";
    const all = el("button", "button", "DOWNLOAD ALL");
    all.style.height = "30px";
    all.style.padding = "0 12px";
    all.style.fontSize = "12px";
    all.addEventListener("click", () => void chooseAll());
    const none = el("button", "button dark", "SKIP ALL");
    none.style.height = "30px";
    none.style.padding = "0 12px";
    none.style.fontSize = "12px";
    none.addEventListener("click", () => void Api.chooseTracks([]).catch(fail));
    b.append(msg, all, none);
    below.prepend(b);
  }
}

/** Picks every track in the current run. */
async function chooseAll() {
  if (!run) return;
  const all = run.tracks.map((_, i) => i);
  try {
    await Api.chooseTracks(all);
  } catch (err) {
    fail(err);
  }
}

function renderFooter(s: RunState) {
  const label = $("#stop-label");
  if (s.phase === "failed" || s.phase === "done" || s.phase === "stopped") {
    label.textContent = "BACK";
  } else if (s.stopped) {
    label.textContent = "STOP NOW";
  } else {
    label.textContent = "STOP";
  }
  $("#foot-log").textContent = lastLog;
}

// ------------------------------------------------------------------ poster

function renderPoster(s: RunState) {
  $("#poster-name").textContent = `Done. ${(s.name ?? "").toUpperCase()}`;

  const list = $("#poster-list");
  list.innerHTML = "";
  const n = Math.max(1, s.tracks.length);

  // The first stripes are the biggest; each one is a little smaller than the
  // one before, and together they fill the window.
  const avail = Math.max(200, list.clientHeight || window.innerHeight - 150);
  const ratio = Math.pow(1 / 3, 1 / Math.max(1, n - 1));
  let sum = 0;
  for (let i = 0; i < n; i++) sum += Math.pow(ratio, i);
  const maxH = n <= 3 ? 220 : 140;

  s.tracks.forEach((t, i) => {
    const h = Math.min(maxH, Math.max(13, Math.floor((avail * Math.pow(ratio, i)) / sum)));
    // The title fills most of its stripe, but never grows past the room it has.
    const fs = Math.max(13, Math.floor(h * 0.62));

    const missing = t.state !== "done";
    const stripe = el("button", "poster-stripe" + (missing ? " gone" : ""));
    stripe.style.height = `${h}px`;
    stripe.style.background = missing ? "#bdbdbb" : stripeAt(i);
    stripe.title = `${t.artist} — ${t.title}`;

    const title = el("span", "stripe-title", t.title.toUpperCase());
    title.style.fontSize = `${fs}px`;
    stripe.append(title, el("span", "grow"));

    if (t.state === "done" && t.source === "youtube") {
      const tag = el("span", "tag", "YT");
      tag.style.fontSize = `${Math.min(18, Math.max(10, fs * 0.4))}px`;
      stripe.append(tag);
    }
    if (missing) {
      const note = el("span", "note", s.stopped ? "NOT DOWNLOADED" : "NOT FOUND");
      note.style.fontSize = `${Math.min(16, Math.max(10, fs * 0.35))}px`;
      stripe.append(note);
    }
    stripe.addEventListener("click", () => {
      if (t.path) {
        Api.openFolder(t.path).catch(fail);
      } else if (t.url) {
        Api.openLink(t.url).catch(fail);
      }
    });
    list.append(stripe);
  });

  const yt = s.tracks.filter((t) => t.state === "done" && t.source === "youtube").length;
  const soulseek = s.tracks.filter((t) => t.state === "done" && t.source !== "youtube").length;
  const missing = s.tracks.filter((t) => t.state !== "done").length;

  let stats = `${s.done ?? 0} ${plural(s.done ?? 0, "track", "tracks")}`;
  if (soulseek > 0) stats += ` · from Soulseek`;
  if (yt > 0) stats += ` · ${yt} from YouTube`;
  if (missing > 0) stats += ` · ${missing} ${s.stopped ? "not downloaded (stopped)" : "missing"}`;
  $("#poster-stats").textContent = stats;
}

// ----------------------------------------------------------------- history

async function renderHistory() {
  const entries = await Api.getHistory();
  const total = entries.reduce((a, e) => a + (e.files ?? 0), 0);
  $("#history-title").textContent = `History · ${total} ${plural(total, "track", "tracks")}`;

  const body = $("#history-body");
  body.innerHTML = "";
  if (entries.length === 0) {
    body.append(el("div", "history-empty", "Nothing downloaded yet"));
    return;
  }
  for (const e of entries) {
    const row = el("div", "sess-row");
    row.append(
      el("span", "sess-name", (e.name || e.link || "").toUpperCase()),
      el("span", "sess-meta", `${kindLabel(e.kind)} · ${e.files ?? 0}/${e.files ?? 0} · ${e.at ? new Date(e.at * 1000).toLocaleDateString() : ""}`),
      el("span", "grow"),
    );
    const folder = el("button", "link-text", "FOLDER ↗");
    folder.addEventListener("click", () => Api.openFolder(e.dir).catch(fail));
    row.append(folder);
    body.append(row);
  }
}

// ---------------------------------------------------------------- settings

function renderSettings() {
  const dir = $<HTMLInputElement>("#dir");
  if (document.activeElement !== dir) dir.value = settings.outputDir;

  const q = $("#quality");
  q.innerHTML = "";
  for (const b of ["320", "256", "192", "128"]) {
    const c = el("button", "choice" + (settings.bitrate === b ? " on" : ""), b);
    c.addEventListener("click", () => {
      settings.bitrate = b;
      renderSettings();
      renderIdle();
    });
    q.append(c);
  }

  const box = $("#fallback-box");
  box.className = "checkbox" + (settings.fallback ? " on" : "");

  const form = $("#login-form");
  form.innerHTML = "";
  const signedIn = settings.wantSoulseek && settings.soulseekUser;
  if (signedIn) {
    const row = el("div", "signed-in");
    row.append(el("span", "signed-in-dot"), el("span", "signed-in-name", `Signed in as ${settings.soulseekUser.toUpperCase()}`));
    const out = el("button", "link-text", "SIGN OUT");
    out.addEventListener("click", async () => {
      try {
        await Api.saveSettings({ ...settings, wantSoulseek: false, soulseekUser: "" });
        settings = await Api.getSettings();
        renderSettings();
        renderIdle();
        toast("Signed out");
      } catch (err) {
        fail(err);
      }
    });
    row.append(out);
    form.append(row);
    return;
  }

  const about = el(
    "p",
    "login-about",
    "Soulseek is a free network where people share music. Gosintify downloads from it with your account. No account yet? Pick a name and a password here: if the name is free, the account is created.",
  );
  const save = el("p", "login-save", "Save the password somewhere: Soulseek cannot reset it.");

  const user = el("input", "field");
  user.id = "soulseek-user";
  user.placeholder = "name";
  user.autocomplete = "off";
  user.autocapitalize = "off";
  user.spellcheck = false;
  const pass = el("input", "field");
  pass.id = "soulseek-pass";
  pass.type = "password";
  pass.placeholder = "password";

  const fields = el("div", "row gap-10");
  const boxedUser = el("div", "boxed grow");
  boxedUser.append(user);
  const boxedPass = el("div", "boxed grow");
  boxedPass.append(pass);
  fields.append(boxedUser, boxedPass);

  const errText = el("p", "login-error");
  errText.hidden = true;

  const go = el("button", "button dark", "SIGN IN / CREATE");
  const hint = el("span", "login-hint", "One account works on one computer at a time.");
  const actions = el("div", "row gap-10");
  actions.append(go, hint);

  const signIn = async () => {
    const u = user.value.trim();
    const p = pass.value;
    if (!u || !p) {
      errText.textContent = "Enter a name and a password.";
      errText.hidden = false;
      return;
    }
    go.disabled = true;
    go.textContent = "SIGNING IN…";
    try {
      const msg = await Api.soulseekLogin(u, p);
      settings = await Api.getSettings();
      renderSettings();
      renderIdle();
      toast(msg || "Signed in");
    } catch (err) {
      errText.textContent = isCallError(err) ? err.message.toUpperCase() : String(err).toUpperCase();
      errText.hidden = false;
      go.disabled = false;
      go.textContent = "SIGN IN / CREATE";
    }
  };
  go.addEventListener("click", () => void signIn());
  user.addEventListener("keydown", (e) => {
    if (e.key === "Enter") void signIn();
  });
  pass.addEventListener("keydown", (e) => {
    if (e.key === "Enter") void signIn();
  });

  form.append(about, save, fields, errText, actions);
}

function openSettings() {
  $("#settings-layer").hidden = false;
  renderSettings();
}

async function saveSettings() {
  const dir = $<HTMLInputElement>("#dir").value.trim();
  if (dir) settings.outputDir = dir;
  try {
    await Api.saveSettings(settings);
    settings = await Api.getSettings();
    renderSettings();
    renderIdle();
    toast("Saved");
  } catch (err) {
    fail(err);
  }
}

function closeSettings() {
  $("#settings-layer").hidden = true;
}

// ------------------------------------------------------------------ events

function pushLog(line: string) {
  logLines.push(line);
  if (logLines.length > 400) logLines = logLines.slice(-400);
  lastLog = line;
}

function applyEvent() {
  const s = run;
  if (!s) return;
  const which = screen();
  show(which);
  if (which === "working") renderWorking(s);
  else if (which === "poster") renderPoster(s);
}

async function refresh() {
  try {
    run = await Api.getState();
  } catch {
    return;
  }
  // The picker: the run waits for the tracks the user chooses.
  if (run.phase === "choosing" && picking.length === 0) {
    picking = run.tracks.map((_, i) => i);
  } else if (run.phase !== "choosing" && picking.length > 0 && run.phase !== "idle") {
    // keep the picks; they are consumed by ChooseTracks
  }
  applyEvent();
}

events.pipeline.on((e: Event) => {
  switch (e.ev) {
    case "log":
      if (e.line) pushLog(e.line);
      break;
    case "collection":
      picking = [];
      break;
    case "error":
      pushLog("ERROR: " + (e.message ?? ""));
      break;
    case "result":
      pushLog(`Done: ${e.files ?? 0} files in ${e.dir ?? ""}`);
      break;
  }
  void refresh();
});

// The Go side announces each phase change; the page then asks for the whole
// state, which is what the poster and the working screen draw.
events.state.on(() => {
  void refresh();
});

// ------------------------------------------------------------------- wiring

function wire() {
  drawGrid();
  window.addEventListener("resize", () => {
    drawGrid();
    if (run && screen() === "poster") renderPoster(run);
    if (run && screen() === "working") renderWorking(run);
  });

  const link = $<HTMLInputElement>("#link");
  link.addEventListener("input", renderIdle);
  link.addEventListener("keydown", (e) => {
    if (e.key === "Enter") void start();
  });
  link.addEventListener("paste", () => window.setTimeout(renderIdle, 0));

  $("#go").addEventListener("click", () => void start());
  $("#where").addEventListener("click", openSettings);
  $("#open-settings").addEventListener("click", openSettings);
  $("#open-history").addEventListener("click", async () => {
    blockedByHistory = true;
    show("history");
    await renderHistory();
  });
  $("#history-back").addEventListener("click", () => {
    blockedByHistory = false;
    run = null;
    show("idle");
    renderIdle();
  });
  $("#no-account").addEventListener("click", openSettings);

  $("#stop").addEventListener("click", () => {
    if (run && (run.phase === "failed" || run.phase === "done" || run.phase === "stopped")) {
      run = null;
      void Api.cancel().catch(() => {});
      show("idle");
      renderIdle();
      return;
    }
    void Api.stop().catch(fail);
  });

  $("#new-link").addEventListener("click", () => {
    run = null;
    void Api.cancel().catch(() => {});
    show("idle");
    renderIdle();
  });

  $("#open-folder").addEventListener("click", () => Api.openFolder(run?.outDir || settings.outputDir).catch(fail));

  $("#save-settings").addEventListener("click", () => void saveSettings());
  $("#settings-scrim").addEventListener("click", closeSettings);
  $("#choose-dir").addEventListener("click", async () => {
    try {
      const dir = await Api.chooseFolder();
      if (dir) {
        $<HTMLInputElement>("#dir").value = dir;
        settings.outputDir = dir;
      }
    } catch (err) {
      fail(err);
    }
  });
  $("#fallback").addEventListener("click", () => {
    settings.fallback = !settings.fallback;
    renderSettings();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !$("#settings-layer").hidden) closeSettings();
  });

  // Paste anywhere on the idle screen starts a download.
  window.addEventListener("paste", (e) => {
    if (screen() !== "idle" || document.activeElement === link) return;
    const text = e.clipboardData?.getData("text") ?? "";
    if (isSpotifyLink(text)) {
      link.value = text.trim();
      renderIdle();
      void start();
    }
  });
}

async function boot() {
  wire();
  settings = await Api.getSettings();
  renderIdle();
  show("idle");
  await refresh();
  if (screen() === "idle" && !blockedByHistory) {
    $<HTMLInputElement>("#link").focus();
  }
}

void boot();
