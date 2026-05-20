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
  const h = location.hostname;
  const looksLikeStaticHost =
    /\.netlify\.app$/i.test(h) ||
    /\.vercel\.app$/i.test(h) ||
    /\.pages\.dev$/i.test(h) ||
    /\.github\.io$/i.test(h);
  if (!looksLikeStaticHost) return;
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
    const err = (data && data.error) || text || res.statusText;
    throw new Error(err);
  }
  return data;
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

function fillRestingPlayerSelect(players) {
  const sel = document.getElementById("resting-player");
  if (!sel) return;
  const prev = sel.value;
  sel.innerHTML = '<option value="">— None —</option>';
  const sorted = [...(players || [])].sort((a, b) =>
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
function aggregatePlayerMatchPoints(matches, players) {
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

  const nMatch = (matches || []).length;
  for (const r of rows) {
    r.sitOuts = nMatch - r.gp;
    r.adjusted = r.scored + r.sitOuts;
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

function renderMatchPointStandings(matches, players) {
  const tb = $("#standings-scored tbody");
  if (!tb) return;
  tb.innerHTML = "";
  const rows = aggregatePlayerMatchPoints(matches, players);
  const hasPlay = rows.some((r) => r.gp > 0);
  if (!hasPlay) {
    const tr = document.createElement("tr");
    tr.innerHTML =
      '<td colspan="5" class="standings-empty">No matches yet. Record a result to see scoring totals.</td>';
    tb.appendChild(tr);
    return;
  }

  const tied = (a, b) =>
    a.adjusted === b.adjusted &&
    a.scored === b.scored &&
    a.gp === b.gp;
  const ranks = addCompetitionRanks(rows, tied);

  for (let i = 0; i < rows.length; i++) {
    const r = rows[i];
    const tr = document.createElement("tr");
    tr.innerHTML = `<td>${ranks[i]}</td><td>${escapeHtml(r.name)}<div class="pid">${escapeHtml(r.id)}</div></td><td>${r.gp}</td><td>${r.scored}</td><td>${r.adjusted}</td>`;
    tb.appendChild(tr);
  }
}

function renderLeagueStandings(matches, players) {
  const tb = $("#standings-league tbody");
  if (!tb) return;
  tb.innerHTML = "";
  const nMatch = (matches || []).length;

  const rows = players.map((p) => {
    const raw = playerPoints(p);
    const sitOuts = nMatch - (p.games_played || 0);
    const adjusted = raw + sitOuts;
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

function renderStandings(sess) {
  renderMatchPointStandings(sess.matches, sess.players);
  renderLeagueStandings(sess.matches, sess.players);
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
  $("#session-meta").textContent =
    `${String(sess.sport).toUpperCase()} · Session ${id}`;
  renderStandings(sess);
  renderSuggestion(sess);
  renderHistory(sess.matches, sess.players);
  fillRestingPlayerSelect(sess.players);
  window.__session = sess;
}

function renderSuggestion(sess) {
  const el = $("#suggestion");
  const sug = sess.suggested;
  if (!sug) {
    el.textContent = "No suggestion yet.";
    window.__suggestion = null;
    return;
  }
  const teamLine = (players) =>
    players.map((x) => `${escapeHtml(x.name)}`).join(" · ");
  el.innerHTML = `
    <div class="team-block" data-side="a">
      <div class="team-title">Team A</div>
      <div>${teamLine(sug.team_a)}</div>
    </div>
    <div class="team-block" data-side="b">
      <div class="team-title">Team B</div>
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
    li.className = "history-item";
    const when = formatHistoryWhenPlayed(m.played_at);
    const sideA = formatHistoryPartners(m.team_a_ids, nameMap);
    const sideB = formatHistoryPartners(m.team_b_ids, nameMap);
    li.innerHTML = `
      <div class="history-when">${escapeHtml(when)}</div>
      <div class="history-row">
        <div class="history-side history-side-a">
          <span class="history-players">${sideA}</span>
          <span class="history-score">(${m.score_a})</span>
        </div>
        <span class="history-vs" aria-hidden="true">vs</span>
        <div class="history-side history-side-b">
          <span class="history-players">${sideB}</span>
          <span class="history-score">(${m.score_b})</span>
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
    toast("Enter scores for both teams");
    return;
  }
  if (score_a < 0 || score_b < 0) {
    toast("Scores must be zero or positive");
    return;
  }
  const body = {
    team_a_ids: sug.team_a.map((p) => p.id),
    team_b_ids: sug.team_b.map((p) => p.id),
    score_a,
    score_b,
  };
  try {
    const updated = await api(`/api/sessions/${sess.id}/matches`, {
      method: "POST",
      body: JSON.stringify(body),
    });
    window.__session = updated;
    renderStandings(updated);
    renderSuggestion(updated);
    renderHistory(updated.matches, updated.players);
    clearScoreFields();
    toast("Match saved");
  } catch (err) {
    toast(err.message);
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
  try {
    const updated = await api(`/api/sessions/${sess.id}/reshuffle`, {
      method: "POST",
      body: payload,
    });
    window.__session = updated;
    renderSuggestion(updated);
    clearScoreFields();
    toast("Lineup updated");
  } catch (err) {
    toast(err.message);
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
    window.__session = updated;
    renderStandings(updated);
    renderSuggestion(updated);
    renderHistory(updated.matches, updated.players);
    fillRestingPlayerSelect(updated.players);
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
  $("#view-home").classList.remove("hidden");
  playerRows(6);
  const path = location.pathname || "/";
  history.replaceState(null, "", path);
}

$("#btn-new-session").addEventListener("click", goHome);

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

function boot() {
  warnIfStaticSiteWithoutApiBase();
  playerRows(6);
  const params = new URLSearchParams(location.search);
  const id = params.get("id");
  if (id) {
    $("#view-home").classList.add("hidden");
    $("#view-session").classList.add("hidden");
    $("#view-loading").classList.remove("hidden");
    $("#view-loading").setAttribute("aria-busy", "true");
    loadSession(id)
      .then(() => {
        $("#view-loading").classList.add("hidden");
        $("#view-loading").setAttribute("aria-busy", "false");
        $("#view-session").classList.remove("hidden");
      })
      .catch((e) => {
        $("#view-loading").classList.add("hidden");
        $("#view-loading").setAttribute("aria-busy", "false");
        toast(e.message);
        $("#view-home").classList.remove("hidden");
      });
  }
}

boot();
