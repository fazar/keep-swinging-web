const $ = (sel) => document.querySelector(sel);

/** Base URL for API (empty = same origin). Set in config.js or by Netlify build. */
function apiBase() {
  const b =
    typeof window !== 'undefined' && window.__KEEP_SWINGING_API_BASE__ != null
      ? String(window.__KEEP_SWINGING_API_BASE__).trim()
      : '';
  return b.replace(/\/$/, '');
}

function apiUrl(path) {
  const p = path.startsWith('/') ? path : `/${path}`;
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
    'Add KEEP_SWINGING_API_BASE (your Render/Fly API URL, no trailing slash) in the host env vars, then redeploy. See DEPLOY.md.';
  console.warn('[Keep Swinging]', detail);
  toast('API URL not set — check Netlify env KEEP_SWINGING_API_BASE + redeploy');
}

function toast(msg) {
  const t = $('#toast');
  t.textContent = msg;
  t.classList.remove('hidden');
  clearTimeout(toast._tid);
  toast._tid = setTimeout(() => t.classList.add('hidden'), 2200);
}

async function api(path, opts = {}) {
  const res = await fetch(apiUrl(path), {
    headers: { 'Content-Type': 'application/json', ...(opts.headers || {}) },
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
  const wrap = $('#players-inputs');
  wrap.innerHTML = '';
  for (let i = 0; i < count; i += 1) {
    const inp = document.createElement('input');
    inp.type = 'text';
    inp.placeholder = `Player ${i + 1}`;
    inp.autocomplete = 'off';
    inp.required = i < 4;
    wrap.appendChild(inp);
  }
}

function escapeHtml(s) {
  const map = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
  return String(s).replace(/[&<>"']/g, (c) => map[c] || c);
}

/** League-style table points: 3 per win, 1 per draw. */
function playerPoints(p) {
  const w = p.wins || 0;
  const d = p.draws || 0;
  return 3 * w + d;
}

function clearScoreFields() {
  const form = document.getElementById('match-form');
  if (!form) return;
  form.elements.score_a.value = '';
  form.elements.score_b.value = '';
}

function resolveExcludeIdsFromRestingName(players, rawName) {
  const q = rawName.trim();
  if (!q) return { ids: [], error: null };
  const lower = q.toLowerCase();
  const hits = players.filter((p) => p.name.trim().toLowerCase() === lower);
  if (hits.length === 0) return { ids: [], error: `No player named "${q}"` };
  if (hits.length > 1) return { ids: [], error: `Multiple players named "${q}" — use unique names` };
  return { ids: [hits[0].id], error: null };
}

function idToNameMap(players) {
  const m = Object.create(null);
  for (const p of players) m[p.id] = p.name;
  return m;
}

const HISTORY_MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
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
  const ap = hr >= 12 ? 'PM' : 'AM';
  hr %= 12;
  if (hr === 0) hr = 12;
  const mm = String(min).padStart(2, '0');
  return `${dom} ${mon} ${yr} ${hr}:${mm} ${ap}`;
}

function formatHistoryPartners(ids, nameMap) {
  return ids.map((id) => escapeHtml(nameMap[id] || id)).join(' & ');
}


async function loadSession(id) {
  const sess = await api(`/api/sessions/${id}`);
  $('#view-home').classList.add('hidden');
  $('#view-session').classList.remove('hidden');
  $('#session-meta').textContent = `${String(sess.sport).toUpperCase()} · Session ${id}`;
  renderStandings(sess.players);
  renderSuggestion(sess);
  renderHistory(sess.matches, sess.players);
  window.__session = sess;
}

function renderStandings(players) {
  const tb = $('#standings tbody');
  tb.innerHTML = '';
  const sorted = [...players].sort((a, b) => {
    const pb = playerPoints(b);
    const pa = playerPoints(a);
    if (pb !== pa) return pb - pa;
    if (b.wins !== a.wins) return b.wins - a.wins;
    const bd = b.draws || 0;
    const ad = a.draws || 0;
    if (bd !== ad) return bd - ad;
    return b.games_played - a.games_played;
  });
  for (const p of sorted) {
    const tr = document.createElement('tr');
    const d = p.draws ?? 0;
    const pts = playerPoints(p);
    tr.innerHTML = `<td>${escapeHtml(p.name)}<div class="pid">${escapeHtml(p.id)}</div></td><td>${p.games_played}</td><td>${p.wins}</td><td>${d}</td><td>${p.losses}</td><td>${pts}</td>`;
    tb.appendChild(tr);
  }
}

function renderSuggestion(sess) {
  const el = $('#suggestion');
  const sug = sess.suggested;
  if (!sug) {
    el.textContent = 'No suggestion yet.';
    window.__suggestion = null;
    return;
  }
  const teamLine = (players) => players.map((x) => `${escapeHtml(x.name)}`).join(' · ');
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
  const ul = $('#history');
  ul.innerHTML = '';
  const nameMap = idToNameMap(players);
  const rev = [...matches].reverse();
  for (const m of rev) {
    const li = document.createElement('li');
    li.className = 'history-item';
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

$('#add-player').addEventListener('click', () => {
  const wrap = $('#players-inputs');
  const n = wrap.querySelectorAll('input').length;
  if (n >= 16) return;
  const inp = document.createElement('input');
  inp.type = 'text';
  inp.placeholder = `Player ${n + 1}`;
  inp.autocomplete = 'off';
  wrap.appendChild(inp);
});

$('#create-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const sport = fd.get('sport');
  const names = [...document.querySelectorAll('#players-inputs input')]
    .map((i) => i.value.trim())
    .filter(Boolean);
  try {
    const sess = await api('/api/sessions', {
      method: 'POST',
      body: JSON.stringify({ sport, players: names }),
    });
    history.replaceState(null, '', `?id=${encodeURIComponent(sess.id)}`);
    await loadSession(sess.id);
    toast('Session started');
  } catch (err) {
    toast(err.message);
  }
});

$('#match-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const sess = window.__session;
  const sug = window.__suggestion;
  if (!sess || !sug) {
    toast('No active suggestion');
    return;
  }
  const fd = new FormData(e.target);
  const score_a = Number(fd.get('score_a'));
  const score_b = Number(fd.get('score_b'));
  if (!Number.isFinite(score_a) || !Number.isFinite(score_b)) {
    toast('Enter scores for both teams');
    return;
  }
  if (score_a < 0 || score_b < 0) {
    toast('Scores must be zero or positive');
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
      method: 'POST',
      body: JSON.stringify(body),
    });
    window.__session = updated;
    renderStandings(updated.players);
    renderSuggestion(updated);
    renderHistory(updated.matches, updated.players);
    clearScoreFields();
    toast('Match saved');
  } catch (err) {
    toast(err.message);
  }
});

$('#btn-reshuffle').addEventListener('click', async () => {
  const sess = window.__session;
  if (!sess) return;
  const restingRaw = document.getElementById('resting-name').value;
  const { ids: excludeIds, error } = resolveExcludeIdsFromRestingName(sess.players, restingRaw);
  if (error) {
    toast(error);
    return;
  }
  const payload = JSON.stringify({ exclude_player_ids: excludeIds });
  try {
    const updated = await api(`/api/sessions/${sess.id}/reshuffle`, {
      method: 'POST',
      body: payload,
    });
    window.__session = updated;
    renderSuggestion(updated);
    clearScoreFields();
    toast('Lineup updated');
  } catch (err) {
    toast(err.message);
  }
});

$('#copy-link').addEventListener('click', async () => {
  const sess = window.__session;
  if (!sess) return;
  const url = `${location.origin}${location.pathname}?id=${encodeURIComponent(sess.id)}`;
  try {
    await navigator.clipboard.writeText(url);
    toast('Link copied');
  } catch (_) {
    toast(url);
  }
});

function boot() {
  warnIfStaticSiteWithoutApiBase();
  playerRows(6);
  const params = new URLSearchParams(location.search);
  const id = params.get('id');
  if (id) {
    loadSession(id).catch((e) => {
      toast(e.message);
      $('#view-home').classList.remove('hidden');
      $('#view-session').classList.add('hidden');
    });
  }
}

boot();
