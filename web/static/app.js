const $ = (sel) => document.querySelector(sel);

/** Base URL for API (empty = same origin). Set in config.js or by Netlify build. */
function apiBase() {
  const b =
    typeof window !== "undefined" && window.__KEEP_SWINGING_API_BASE__ != null
      ? String(window.__KEEP_SWINGING_API_BASE__).trim()
      : "";
  return b.replace(/\/$/, "");
}

function apiUrl(path) {
  const p = path.startsWith("/") ? path : `/${path}`;
  const base = apiBase();
  return base ? `${base}${p}` : p;
}

/** Netlify/etc. only serve static files; /api must go to the Go backend. */
function warnIfStaticSiteWithoutApiBase() {
  if (apiBase()) return;
  if (!looksLikeStaticHostOnly()) return;
  const detail =
    "Add KEEP_SWINGING_API_BASE (your Render/Fly API URL, no trailing slash) in the host env vars, then redeploy. See DEPLOY.md.";
  console.warn("[Keep Swinging]", detail);
  toast(
    "API URL not set — check Netlify env KEEP_SWINGING_API_BASE + redeploy",
  );
}

function toast(msg) {
  const t = $("#toast");
  t.textContent = msg;
  t.classList.remove("hidden");
  clearTimeout(toast._tid);
  toast._tid = setTimeout(() => t.classList.add("hidden"), 2200);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/** Keep suggestion loading visible at least this long (fast APIs otherwise flash). */
const LINEUP_ACTION_MIN_MS = 500;

async function withMinDelay(ms, fn) {
  const t0 = performance.now();
  try {
    const result = await fn();
    const left = ms - (performance.now() - t0);
    if (left > 0) await delay(left);
    return result;
  } catch (err) {
    const left = ms - (performance.now() - t0);
    if (left > 0) await delay(left);
    throw err;
  }
}

async function api(path, opts = {}) {
  const res = await fetch(apiUrl(path), {
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  const text = await res.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch (_) {
    /* ignore */
  }
  if (!res.ok) {
    const msg =
      (data && typeof data.error === "string" && data.error) ||
      text ||
      res.statusText;
    const err = new Error(msg);
    err.status = res.status;
    throw err;
  }
  return data;
}

function looksLikeStaticHostOnly() {
  const h = location.hostname;
  return (
    /\.netlify\.app$/i.test(h) ||
    /\.vercel\.app$/i.test(h) ||
    /\.pages\.dev$/i.test(h) ||
    /\.github\.io$/i.test(h)
  );
}

/** When true, show loading until /api/health succeeds (cold start on Render, etc.). */
function shouldPingApi() {
  if (apiBase()) return true;
  return !looksLikeStaticHostOnly();
}

function setLoadingUi(kind) {
  const title = document.getElementById("loading-title");
  const hint = document.getElementById("loading-hint");
  if (!title || !hint) return;
  if (kind === "session") {
    title.textContent = "Loading session…";
    hint.textContent = "Blackbird singing in the dead of night..";
  } else {
    title.textContent = "Connecting to server…";
    hint.textContent = "Hey antek antek async..";
  }
}

const API_READY_ATTEMPTS = 35;
const API_READY_DELAY_MS = 2000;

async function waitForApiReady() {
  let lastErr;
  for (let i = 0; i < API_READY_ATTEMPTS; i++) {
    try {
      const res = await fetch(apiUrl("/api/health"), {
        method: "GET",
        cache: "no-store",
      });
      if (res.ok) return;
      let detail = "";
      try {
        detail = await res.text();
      } catch (_) {
        /* ignore */
      }
      lastErr = new Error(detail || `HTTP ${res.status}`);
    } catch (e) {
      lastErr = e instanceof Error ? e : new Error(String(e));
    }
    await new Promise((r) => setTimeout(r, API_READY_DELAY_MS));
  }
  throw lastErr instanceof Error
    ? lastErr
    : new Error("Server did not become ready in time.");
}

function playerRows(count) {
  const wrap = $("#players-inputs");
  wrap.innerHTML = "";
  for (let i = 0; i < count; i += 1) {
    const inp = document.createElement("input");
    inp.type = "text";
    inp.placeholder = `Player ${i + 1}`;
    inp.autocomplete = "off";
    inp.required = i < 4;
    wrap.appendChild(inp);
  }
}

function escapeHtml(s) {
  const map = {
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  };
  return String(s).replace(/[&<>"']/g, (c) => map[c] || c);
}

/** League-style table points: 3 per win, 1 per draw. */
function playerPoints(p) {
  const w = p.wins || 0;
  const d = p.draws || 0;
  return 3 * w + d;
}

function clearScoreFields() {
  const form = document.getElementById("match-form");
  if (!form) return;
  form.elements.score_a.value = "";
  form.elements.score_b.value = "";
}

function idToNameMap(players) {
  const m = Object.create(null);
  for (const p of players) m[p.id] = p.name;
  return m;
}

const MAX_PLAYERS_SESSION = 16;

function shuffleActivePlayers(players) {
  return (players || []).filter((p) => !p.inactive);
}

function namesEquivalent(a, b) {
  return (
    String(a ?? "")
      .trim()
      .toLowerCase() ===
    String(b ?? "")
      .trim()
      .toLowerCase()
  );
}

/** Default parity multiplier k by sport when sit_out_score is not stored yet. */
function defaultSitOutScoreForSport(sport) {
  const s = String(sport ?? "").toLowerCase();
  if (s === "padel") return 10;
  if (s === "tennis") return 2;
  return 2;
}

/** Session parity multiplier k (Adj includes k × GP gap vs leader). */
function effectiveSitOutScore(sess) {
  if (!sess) return defaultSitOutScoreForSport("");
  if (sess.sit_out_score != null) {
    const x = Number(sess.sit_out_score);
    if (!Number.isFinite(x) || x < 0) {
      return defaultSitOutScoreForSport(sess.sport);
    }
    return x;
  }
  return defaultSitOutScoreForSport(sess.sport);
}

function doublesNamesParen(players) {
  if (!players || !players.length) return "";
  return players.map((p) => (p.name || "").trim() || "?").join(" · ");
}

function updateRecordMatchLabels(sess) {
  const leftLbl = $("#match-label-left");
  const rightLbl = $("#match-label-right");
  if (!leftLbl || !rightLbl) return;
  const sug = sess?.suggested;
  if (!sug?.team_a?.length || !sug?.team_b?.length) {
    leftLbl.textContent = "Team Left score";
    rightLbl.textContent = "Team Right score";
    return;
  }
  leftLbl.textContent = `Team Left (${doublesNamesParen(sug.team_a)})`;
  rightLbl.textContent = `Team Right (${doublesNamesParen(sug.team_b)})`;
}

function showSuggestionLoading(message) {
  const el = $("#suggestion");
  if (!el) return;
  el.setAttribute("aria-busy", "true");
  const msg = escapeHtml(message);
  el.innerHTML = `
    <div class="suggestion-loading" role="status" aria-live="polite">
      <span class="app-spinner app-spinner--md" aria-hidden="true"></span>
      <p class="suggestion-loading-text">${msg}</p>
    </div>
  `;
}

function setLineupControlsBusy(busy) {
  const reshuffle = $("#btn-reshuffle");
  const saveMatch = $("#btn-save-match");
  const form = $("#match-form");
  if (reshuffle) reshuffle.disabled = !!busy;
  if (saveMatch) saveMatch.disabled = !!busy;
  if (form) {
    for (const inp of form.querySelectorAll('input[type="number"]')) {
      inp.disabled = !!busy;
    }
  }
}

function refreshSessionView(sess) {
  window.__session = sess;
  $("#session-meta").textContent =
    `${String(sess.sport).toUpperCase()} · Session ${sess.id}`;
  const k = effectiveSitOutScore(sess);
  renderStandings(sess, k);
  renderSuggestion(sess);
  updateRecordMatchLabels(sess);
  renderHistory(sess.matches, sess.players);
  fillRestingPlayerSelect(sess.players);
  renderSessionSetup(sess);
  setLineupControlsBusy(false);
}

function fillRestingPlayerSelect(players) {
  const sel = document.getElementById("resting-player");
  if (!sel) return;
  const prev = sel.value;
  sel.innerHTML = '<option value="">— None —</option>';
  const sorted = [...shuffleActivePlayers(players)].sort((a, b) =>
    a.name.localeCompare(b.name),
  );
  for (const p of sorted) {
    const opt = document.createElement("option");
    opt.value = p.id;
    opt.textContent = p.name;
    sel.appendChild(opt);
  }
  if (prev && [...sel.options].some((o) => o.value === prev)) {
    sel.value = prev;
  } else {
    sel.value = "";
  }
}

/** Per-player totals: each game adds the team's score to every player on that team. */
function aggregatePlayerMatchPoints(matches, players, parityK) {
  const rows = players.map((p) => ({
    id: p.id,
    name: p.name,
    gp: 0,
    scored: 0,
  }));
  const byId = Object.create(null);
  for (const r of rows) byId[r.id] = r;

  for (const m of matches || []) {
    const sa = Number(m.score_a) || 0;
    const sb = Number(m.score_b) || 0;
    for (const id of m.team_a_ids || []) {
      const r = byId[id];
      if (r) {
        r.gp += 1;
        r.scored += sa;
      }
    }
    for (const id of m.team_b_ids || []) {
      const r = byId[id];
      if (r) {
        r.gp += 1;
        r.scored += sb;
      }
    }
  }

  const maxGp = rows.length
    ? Math.max(...rows.map((r) => r.gp))
    : 0;
  const k = Number(parityK);
  const mult = Number.isFinite(k) && k >= 0 ? k : 2;
  for (const r of rows) {
    r.adjusted = r.scored + mult * (maxGp - r.gp);
  }

  rows.sort((a, b) => {
    if (b.adjusted !== a.adjusted) return b.adjusted - a.adjusted;
    if (b.scored !== a.scored) return b.scored - a.scored;
    if (b.gp !== a.gp) return b.gp - a.gp;
    return a.name.localeCompare(b.name);
  });
  return rows;
}

function addCompetitionRanks(sorted, tiedFn) {
  const ranks = new Array(sorted.length);
  for (let i = 0; i < sorted.length; i++) {
    if (i === 0) ranks[i] = 1;
    else if (tiedFn(sorted[i], sorted[i - 1])) ranks[i] = ranks[i - 1];
    else ranks[i] = i + 1;
  }
  return ranks;
}

function renderMatchPointStandings(matches, players, parityK) {
  const tb = $("#standings-scored tbody");
  if (!tb) return;
  tb.innerHTML = "";
  const rows = aggregatePlayerMatchPoints(matches, players, parityK);
  const hasPlay = rows.some((r) => r.gp > 0);
  if (!hasPlay) {
    const tr = document.createElement("tr");
    tr.innerHTML =
      '<td colspan="5" class="standings-empty">No matches yet. Record a result to see scoring totals.</td>';
    tb.appendChild(tr);
    return;
  }

  const tied = (a, b) =>
    a.adjusted === b.adjusted && a.scored === b.scored && a.gp === b.gp;
  const ranks = addCompetitionRanks(rows, tied);

  for (let i = 0; i < rows.length; i++) {
    const r = rows[i];
    const tr = document.createElement("tr");
    tr.innerHTML = `<td>${ranks[i]}</td><td>${escapeHtml(r.name)}<div class="pid">${escapeHtml(r.id)}</div></td><td>${r.gp}</td><td>${r.scored}</td><td>${r.adjusted}</td>`;
    tb.appendChild(tr);
  }
}

function renderLeagueStandings(_matches, players, parityK) {
  const tb = $("#standings-league tbody");
  if (!tb) return;
  tb.innerHTML = "";
  const maxGp =
    players.length === 0
      ? 0
      : Math.max(...players.map((p) => p.games_played || 0));
  const k = Number(parityK);
  const mult = Number.isFinite(k) && k >= 0 ? k : 2;

  const rows = players.map((p) => {
    const raw = playerPoints(p);
    const gp = p.games_played || 0;
    const adjusted = raw + mult * (maxGp - gp);
    return { p, raw, adjusted };
  });

  const sorted = rows.sort((a, b) => {
    if (b.adjusted !== a.adjusted) return b.adjusted - a.adjusted;
    if (b.raw !== a.raw) return b.raw - a.raw;
    if (b.p.wins !== a.p.wins) return b.p.wins - a.p.wins;
    const bd = b.p.draws || 0;
    const ad = a.p.draws || 0;
    if (bd !== ad) return bd - ad;
    if (b.p.losses !== a.p.losses) return b.p.losses - a.p.losses;
    if (b.p.games_played !== a.p.games_played)
      return b.p.games_played - a.p.games_played;
    return a.p.name.localeCompare(b.p.name);
  });

  const leagueTied = (a, b) =>
    a.adjusted === b.adjusted &&
    a.raw === b.raw &&
    a.p.wins === b.p.wins &&
    (a.p.draws || 0) === (b.p.draws || 0) &&
    a.p.losses === b.p.losses &&
    a.p.games_played === b.p.games_played;
  const ranks = addCompetitionRanks(sorted, leagueTied);

  for (let i = 0; i < sorted.length; i++) {
    const { p, raw, adjusted } = sorted[i];
    const tr = document.createElement("tr");
    const d = p.draws ?? 0;
    tr.innerHTML = `<td>${ranks[i]}</td><td>${escapeHtml(p.name)}<div class="pid">${escapeHtml(p.id)}</div></td><td>${p.games_played}</td><td>${p.wins}</td><td>${d}</td><td>${p.losses}</td><td>${raw}</td><td>${adjusted}</td>`;
    tb.appendChild(tr);
  }
}

function renderStandings(sess, parityK = effectiveSitOutScore(sess)) {
  renderMatchPointStandings(sess.matches, sess.players, parityK);
  renderLeagueStandings(sess.matches, sess.players, parityK);
}

function renderSessionSetup(sess) {
  const scoreInp = $("#config-sit-out-score");
  if (scoreInp) scoreInp.value = String(effectiveSitOutScore(sess));

  const plist = sess.players || [];
  const activePl = shuffleActivePlayers(plist);
  const activeInShuffle = activePl.length;
  const canRemoveFromShuffle = activeInShuffle > 4;

  const ul = $("#session-setup-player-list");
  if (ul) {
    ul.innerHTML = "";
    const sorted = [...plist].sort((a, b) => {
      const away = Number(!!a.inactive) - Number(!!b.inactive);
      if (away !== 0) return away;
      return a.name.localeCompare(b.name);
    });
    for (const p of sorted) {
      const inactive = !!p.inactive;
      const li = document.createElement("li");
      li.className = `session-setup-player-item${inactive ? " session-setup-player-item--inactive" : ""}`;
      let actionHtml;
      if (inactive) {
        actionHtml =
          '<span class="session-setup-away">Away — enter the same name above to bring them back</span>';
      } else if (!canRemoveFromShuffle) {
        actionHtml =
          '<span class="session-setup-away" title="Need at least four people in shuffle for doubles">Can’t remove — only four in shuffle</span>';
      } else {
        actionHtml = `<button type="button" class="secondary session-remove-player" data-player-id="${escapeHtml(p.id)}" aria-label="Remove ${escapeHtml(p.name)} from shuffle">Remove</button>`;
      }
      li.innerHTML = `<div class="session-setup-player-main"><span class="session-setup-player-name">${escapeHtml(p.name)}</span><span class="pid">${escapeHtml(p.id)}</span></div><div class="session-setup-player-action">${actionHtml}</div>`;
      ul.appendChild(li);
    }
  }

  const n = plist.length;
  const rosterFull = n >= MAX_PLAYERS_SESSION;
  const hasInactive = plist.some((p) => p.inactive);
  const countMeta = $("#session-player-count-meta");
  if (countMeta) {
    countMeta.textContent = `${activeInShuffle} in shuffle · ${n} on roster · max roster ${MAX_PLAYERS_SESSION}`;
  }

  const cantAddFresh = rosterFull && !hasInactive;
  const nameInput = $("#session-new-player-name");
  const btnAdd = $("#btn-add-session-player");
  const hint = $("#session-setup-player-hint");
  if (nameInput) nameInput.disabled = cantAddFresh;
  if (btnAdd) btnAdd.disabled = cantAddFresh;
  if (hint) {
    if (cantAddFresh) {
      hint.textContent = `Everyone is still in shuffle and the roster is full (${MAX_PLAYERS_SESSION}); mark someone as away first before you can add anyone.`;
    } else if (rosterFull && hasInactive) {
      hint.textContent = `All ${MAX_PLAYERS_SESSION} roster spots are filled. New names aren’t accepted—only bring back someone who is away using their exact same name (case-insensitive).`;
    } else {
      hint.textContent =
        "Away players keep history and standings. Add someone new if you have roster space, or reuse an away player’s exact name to put them back in the shuffle.";
    }
  }
}

function wireStandingsTabs() {
  const tabScored = $("#tab-standings-scored");
  const tabLeague = $("#tab-standings-league");
  const panelScored = $("#standings-panel-scored");
  const panelLeague = $("#standings-panel-league");
  const hintScored = $("#standings-hint-scored");
  const hintLeague = $("#standings-hint-league");
  if (!tabScored || !tabLeague) return;

  function activateMatchPoints(on) {
    if (on) {
      tabScored.classList.add("standings-tab--active");
      tabLeague.classList.remove("standings-tab--active");
      tabScored.setAttribute("aria-selected", "true");
      tabLeague.setAttribute("aria-selected", "false");
      panelScored?.classList.remove("hidden");
      panelLeague?.classList.add("hidden");
      hintScored?.classList.remove("hidden");
      hintLeague?.classList.add("hidden");
    } else {
      tabLeague.classList.add("standings-tab--active");
      tabScored.classList.remove("standings-tab--active");
      tabLeague.setAttribute("aria-selected", "true");
      tabScored.setAttribute("aria-selected", "false");
      panelLeague?.classList.remove("hidden");
      panelScored?.classList.add("hidden");
      hintLeague?.classList.remove("hidden");
      hintScored?.classList.add("hidden");
    }
  }

  tabScored.addEventListener("click", () => activateMatchPoints(true));
  tabLeague.addEventListener("click", () => activateMatchPoints(false));
}

function wireSessionSetupTabs() {
  const tabConfig = $("#tab-session-setup-config");
  const tabPlayers = $("#tab-session-setup-players");
  const panelConfig = $("#session-setup-panel-config");
  const panelPlayers = $("#session-setup-panel-players");
  if (!tabConfig || !tabPlayers || !panelConfig || !panelPlayers) return;

  function activate(which) {
    if (which === "config") {
      tabConfig.classList.add("standings-tab--active");
      tabPlayers.classList.remove("standings-tab--active");
      tabConfig.setAttribute("aria-selected", "true");
      tabPlayers.setAttribute("aria-selected", "false");
      panelConfig.classList.remove("hidden");
      panelPlayers.classList.add("hidden");
    } else {
      tabPlayers.classList.add("standings-tab--active");
      tabConfig.classList.remove("standings-tab--active");
      tabPlayers.setAttribute("aria-selected", "true");
      tabConfig.setAttribute("aria-selected", "false");
      panelPlayers.classList.remove("hidden");
      panelConfig.classList.add("hidden");
    }
  }

  tabConfig.addEventListener("click", () => activate("config"));
  tabPlayers.addEventListener("click", () => activate("players"));
}

const HISTORY_MONTHS = [
  "January",
  "February",
  "March",
  "April",
  "May",
  "June",
  "July",
  "August",
  "September",
  "October",
  "November",
  "December",
];

function ordinalDay(n) {
  const j = n % 10;
  const k = n % 100;
  if (k >= 11 && k <= 13) return `${n}th`;
  if (j === 1) return `${n}st`;
  if (j === 2) return `${n}nd`;
  if (j === 3) return `${n}rd`;
  return `${n}th`;
}

/** e.g. "19th May 2026 9:48 AM" in local time */
function formatHistoryWhenPlayed(isoString) {
  const d = new Date(isoString);
  const dom = ordinalDay(d.getDate());
  const mon = HISTORY_MONTHS[d.getMonth()];
  const yr = d.getFullYear();
  let hr = d.getHours();
  const min = d.getMinutes();
  const ap = hr >= 12 ? "PM" : "AM";
  hr %= 12;
  if (hr === 0) hr = 12;
  const mm = String(min).padStart(2, "0");
  return `${dom} ${mon} ${yr} ${hr}:${mm} ${ap}`;
}

function formatHistoryPartners(ids, nameMap) {
  return ids.map((id) => escapeHtml(nameMap[id] || id)).join(" & ");
}

async function loadSession(id) {
  const sess = await api(`/api/sessions/${id}`);
  refreshSessionView(sess);
}

function renderSuggestion(sess) {
  const el = $("#suggestion");
  if (!el) return;
  el.removeAttribute("aria-busy");
  const sug = sess?.suggested;
  if (!sug) {
    el.textContent = "No suggestion yet.";
    window.__suggestion = null;
    return;
  }
  const teamLine = (players) =>
    players.map((x) => `${escapeHtml(x.name)}`).join(" · ");
  el.innerHTML = `
    <div class="team-block" data-side="left">
      <div class="team-title">Team Left</div>
      <div>${teamLine(sug.team_a)}</div>
    </div>
    <div class="team-block" data-side="right">
      <div class="team-title">Team Right</div>
      <div>${teamLine(sug.team_b)}</div>
    </div>
  `;
  window.__suggestion = sug;
}

function renderHistory(matches, players) {
  const ul = $("#history");
  ul.innerHTML = "";
  const nameMap = idToNameMap(players);
  const rev = [...matches].reverse();
  for (const m of rev) {
    const li = document.createElement("li");
    const when = formatHistoryWhenPlayed(m.played_at);
    const sideA = formatHistoryPartners(m.team_a_ids, nameMap);
    const sideB = formatHistoryPartners(m.team_b_ids, nameMap);
    const sa = Number(m.score_a) || 0;
    const sb = Number(m.score_b) || 0;
    let outcomeAria = "";
    let itemMods = "";
    if (sa > sb) {
      itemMods = " history-item--win-left";
      outcomeAria = "Team Left won.";
    } else if (sb > sa) {
      itemMods = " history-item--win-right";
      outcomeAria = "Team Right won.";
    } else {
      outcomeAria = "Draw.";
    }
    li.className = `history-item${itemMods}`;
    li.setAttribute(
      "aria-label",
      `${when} ${outcomeAria} Score ${sa}–${sb}.`,
    );
    li.innerHTML = `
      <div class="history-when">${escapeHtml(when)}</div>
      <div class="history-row">
        <div class="history-side history-side-a">
          <span class="history-side-label">Team Left</span>
          <span class="history-players">${sideA}</span>
          <span class="history-score">${sa}</span>
        </div>
        <span class="history-vs" aria-hidden="true">vs</span>
        <div class="history-side history-side-b">
          <span class="history-side-label">Team Right</span>
          <span class="history-players">${sideB}</span>
          <span class="history-score">${sb}</span>
        </div>
      </div>
    `;
    ul.appendChild(li);
  }
}

$("#add-player").addEventListener("click", () => {
  const wrap = $("#players-inputs");
  const n = wrap.querySelectorAll("input").length;
  if (n >= 16) return;
  const inp = document.createElement("input");
  inp.type = "text";
  inp.placeholder = `Player ${n + 1}`;
  inp.autocomplete = "off";
  wrap.appendChild(inp);
});

$("#create-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const sport = fd.get("sport");
  const names = [...document.querySelectorAll("#players-inputs input")]
    .map((i) => i.value.trim())
    .filter(Boolean);
  try {
    const sess = await api("/api/sessions", {
      method: "POST",
      body: JSON.stringify({ sport, players: names }),
    });
    history.replaceState(null, "", `?id=${encodeURIComponent(sess.id)}`);
    $("#view-home").classList.add("hidden");
    await loadSession(sess.id);
    $("#view-session").classList.remove("hidden");
    toast("Session started");
  } catch (err) {
    toast(err.message);
  }
});

$("#match-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const sess = window.__session;
  const sug = window.__suggestion;
  if (!sess || !sug) {
    toast("No active suggestion");
    return;
  }
  const fd = new FormData(e.target);
  const score_a = Number(fd.get("score_a"));
  const score_b = Number(fd.get("score_b"));
  if (!Number.isFinite(score_a) || !Number.isFinite(score_b)) {
    toast("Enter scores for Team Left and Team Right");
    return;
  }
  if (score_a < 0 || score_b < 0) {
    toast("Scores must be zero or positive");
    return;
  }
  const prevSess = window.__session;
  const body = {
    team_a_ids: sug.team_a.map((p) => p.id),
    team_b_ids: sug.team_b.map((p) => p.id),
    score_a,
    score_b,
  };
  showSuggestionLoading("Saving match…");
  setLineupControlsBusy(true);
  try {
    const updated = await withMinDelay(LINEUP_ACTION_MIN_MS, () =>
      api(`/api/sessions/${sess.id}/matches`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    );
    refreshSessionView(updated);
    clearScoreFields();
    toast("Match saved");
  } catch (err) {
    toast(err.message);
    if (prevSess) {
      renderSuggestion(prevSess);
      updateRecordMatchLabels(prevSess);
    }
  } finally {
    setLineupControlsBusy(false);
  }
});

$("#btn-reshuffle").addEventListener("click", async () => {
  const sess = window.__session;
  if (!sess) return;
  const sel = document.getElementById("resting-player");
  const rid = sel && sel.value ? String(sel.value).trim() : "";
  const idSet = new Set(sess.players.map((p) => p.id));
  const excludeIds = rid && idSet.has(rid) ? [rid] : [];
  const payload = JSON.stringify({ exclude_player_ids: excludeIds });
  showSuggestionLoading("Reshuffling lineup…");
  setLineupControlsBusy(true);
  try {
    const updated = await withMinDelay(LINEUP_ACTION_MIN_MS, () =>
      api(`/api/sessions/${sess.id}/reshuffle`, {
        method: "POST",
        body: payload,
      }),
    );
    window.__session = updated;
    renderSuggestion(updated);
    updateRecordMatchLabels(updated);
    renderSessionSetup(updated);
    clearScoreFields();
    toast("Lineup updated");
  } catch (err) {
    toast(err.message);
    renderSuggestion(sess);
    updateRecordMatchLabels(sess);
  } finally {
    setLineupControlsBusy(false);
  }
});

$("#btn-reset-scores").addEventListener("click", async () => {
  const sess = window.__session;
  if (!sess) return;
  const ok = window.confirm(
    "Reset all match history and standings for this session? Players stay the same.",
  );
  if (!ok) return;
  try {
    const updated = await api(`/api/sessions/${sess.id}/reset`, {
      method: "POST",
      body: "{}",
    });
    refreshSessionView(updated);
    clearScoreFields();
    toast("Standings and history cleared");
  } catch (err) {
    toast(err.message);
  }
});

function goHome() {
  window.__session = null;
  window.__suggestion = null;
  $("#view-session").classList.add("hidden");
  $("#view-loading").classList.add("hidden");
  $("#view-not-found")?.classList.add("hidden");
  const path = location.pathname || "/";
  history.replaceState(null, "", path);

  if (shouldPingApi()) {
    $("#view-home").classList.add("hidden");
    $("#view-loading").classList.remove("hidden");
    $("#view-loading").setAttribute("aria-busy", "true");
    setLoadingUi("wake");
    waitForApiReady()
      .then(() => {
        $("#view-loading").classList.add("hidden");
        $("#view-loading").setAttribute("aria-busy", "false");
        $("#view-not-found")?.classList.add("hidden");
        $("#view-home").classList.remove("hidden");
        playerRows(6);
      })
      .catch(() => {
        $("#view-loading").classList.add("hidden");
        $("#view-loading").setAttribute("aria-busy", "false");
        $("#view-not-found")?.classList.add("hidden");
        $("#view-home").classList.remove("hidden");
        playerRows(6);
        toast(
          "Could not reach the server yet. Wait a moment and try New session again.",
        );
      });
  } else {
    $("#view-home").classList.remove("hidden");
    playerRows(6);
  }
}

$("#btn-new-session").addEventListener("click", goHome);

$("#btn-not-found-home")?.addEventListener("click", () => {
  goHome();
});

$("#copy-link").addEventListener("click", async () => {
  const sess = window.__session;
  if (!sess) return;
  const url = `${location.origin}${location.pathname}?id=${encodeURIComponent(sess.id)}`;
  try {
    await navigator.clipboard.writeText(url);
    toast("Link copied");
  } catch (_) {
    toast(url);
  }
});

wireStandingsTabs();
wireSessionSetupTabs();

$("#form-session-config")?.addEventListener("submit", async (e) => {
  e.preventDefault();
  const sess = window.__session;
  if (!sess) return;
  const fd = new FormData(e.target);
  const raw = fd.get("sit_out_score");
  const sit = Number(raw);
  if (!Number.isFinite(sit) || sit < 0) {
    toast("Enter a valid non‑negative multiplier (k)");
    return;
  }
  try {
    const updated = await api(`/api/sessions/${sess.id}/config`, {
      method: "PATCH",
      body: JSON.stringify({ sit_out_score: sit }),
    });
    refreshSessionView(updated);
    toast("Config saved");
  } catch (err) {
    toast(err.message);
  }
});

$("#form-add-session-player")?.addEventListener("submit", async (e) => {
  e.preventDefault();
  const sess = window.__session;
  if (!sess) return;
  const fd = new FormData(e.target);
  const name = String(fd.get("name") ?? "").trim();
  if (!name) {
    toast("Enter a player name");
    return;
  }
  const plist = sess.players || [];
  const reviving = plist.some(
    (p) => p.inactive && namesEquivalent(p.name, name),
  );
  if (plist.length >= MAX_PLAYERS_SESSION && !reviving) {
    toast(`Roster is full (${MAX_PLAYERS_SESSION} players)`);
    return;
  }
  try {
    const updated = await api(`/api/sessions/${sess.id}/players`, {
      method: "POST",
      body: JSON.stringify({ name }),
    });
    refreshSessionView(updated);
    const nameIn = $("#session-new-player-name");
    if (nameIn) nameIn.value = "";
    toast(reviving ? `${name} is back in the shuffle` : `Added ${name}`);
  } catch (err) {
    toast(err.message);
  }
});

$("#session-setup-player-list")?.addEventListener("click", async (ev) => {
  const btn = ev.target.closest(".session-remove-player");
  if (!btn || btn.disabled) return;
  const sess = window.__session;
  if (!sess) return;
  const pid = btn.getAttribute("data-player-id");
  if (!pid) return;
  const ok = window.confirm(
    "Remove this player from the shuffle? Their past matches and standings stay.",
  );
  if (!ok) return;
  try {
    const updated = await api(`/api/sessions/${sess.id}/players/${pid}`, {
      method: "DELETE",
    });
    refreshSessionView(updated);
    toast("Player removed from shuffle");
  } catch (err) {
    toast(err.message);
  }
});

function boot() {
  warnIfStaticSiteWithoutApiBase();
  const params = new URLSearchParams(location.search);
  const id = params.get("id");
  if (id) {
    $("#view-home").classList.add("hidden");
    $("#view-session").classList.add("hidden");
    $("#view-loading").classList.remove("hidden");
    $("#view-loading").setAttribute("aria-busy", "true");
    if (shouldPingApi()) {
      setLoadingUi("wake");
    } else {
      setLoadingUi("session");
    }
    const startLoad = () => {
      setLoadingUi("session");
      return loadSession(id);
    };
    const pipeline = shouldPingApi()
      ? waitForApiReady().then(startLoad)
      : startLoad();
    pipeline
      .then(() => {
        $("#view-loading").classList.add("hidden");
        $("#view-loading").setAttribute("aria-busy", "false");
        $("#view-not-found")?.classList.add("hidden");
        $("#view-session").classList.remove("hidden");
      })
      .catch((e) => {
        $("#view-loading").classList.add("hidden");
        $("#view-loading").setAttribute("aria-busy", "false");
        if (e && typeof e.status === "number" && e.status === 404 && id) {
          window.__session = null;
          window.__suggestion = null;
          $("#view-session").classList.add("hidden");
          $("#view-home").classList.add("hidden");
          $("#view-not-found")?.classList.remove("hidden");
          history.replaceState(null, "", location.pathname || "/");
          return;
        }
        toast(e.message);
        $("#view-not-found")?.classList.add("hidden");
        $("#view-home").classList.remove("hidden");
      });
    return;
  }

  if (shouldPingApi()) {
    $("#view-home").classList.add("hidden");
    $("#view-loading").classList.remove("hidden");
    $("#view-loading").setAttribute("aria-busy", "true");
    setLoadingUi("wake");
    waitForApiReady()
      .then(() => {
        $("#view-loading").classList.add("hidden");
        $("#view-loading").setAttribute("aria-busy", "false");
        $("#view-not-found")?.classList.add("hidden");
        $("#view-home").classList.remove("hidden");
        playerRows(6);
      })
      .catch(() => {
        $("#view-loading").classList.add("hidden");
        $("#view-loading").setAttribute("aria-busy", "false");
        $("#view-not-found")?.classList.add("hidden");
        $("#view-home").classList.remove("hidden");
        playerRows(6);
        toast(
          "Could not reach the server yet. Check the API URL or wait and refresh.",
        );
      });
  } else {
    playerRows(6);
  }
}

boot();
