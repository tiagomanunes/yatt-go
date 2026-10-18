'use strict';

// ---- small helpers ----------------------------------------------------------

const $ = (sel) => document.querySelector(sel);
const pad = (n) => String(n).padStart(2, '0');
const mod = (n, m) => ((n % m) + m) % m;

const fmtTime = (mins) => {
  const m = mod(mins, 1440);
  return `${pad(Math.floor(m / 60))}:${pad(m % 60)}`;
};
const parseTime = (s) => {
  const [h, m] = s.split(':').map(Number);
  return h * 60 + m;
};
const fmtDate = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const byName = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

async function request(method, url, body) {
  const init = { method };
  if (method !== 'GET') {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body ?? {});
  }
  const res = await fetch(url, init);
  if (!res.ok) {
    let msg = res.statusText;
    try { msg = (await res.json()).error || msg; } catch { /* not JSON */ }
    throw new Error(msg);
  }
  return res.json();
}
const api = {
  get: (url) => request('GET', url),
  post: (url, body) => request('POST', url, body),
  put: (url, body) => request('PUT', url, body),
};

window.addEventListener('unhandledrejection', (e) => {
  console.error(e.reason);
  alert(`Something went wrong: ${e.reason?.message ?? e.reason}`);
});

// ---- main view --------------------------------------------------------------

const dateInput = $('#date');
const panes = { planned: $('#planned'), done: $('#done') };

dateInput.value = fmtDate(new Date());
dateInput.addEventListener('change', loadWork);

async function loadWork() {
  const date = dateInput.value;
  if (!date) return;
  const [planned, done] = await Promise.all([
    api.get(`/api/activities?kind=planned&date=${date}`),
    api.get(`/api/activities?kind=done&date=${date}`),
  ]);
  if (date !== dateInput.value) return; // the date changed while loading
  panes.planned.replaceChildren(...planned.map(renderEntry));
  panes.done.replaceChildren(...done.map(renderEntry));
}

function renderEntry(act) {
  const el = document.createElement('div');
  el.className = 'entry';
  el.innerHTML = '<div class="row"><span class="start"></span><span class="cat"></span></div><div class="name"></div>';
  const start = el.querySelector('.start');
  const cat = el.querySelector('.cat');
  const name = el.querySelector('.name');

  const refresh = () => {
    start.textContent = act.startTime;
    if (act.duration < 30) {
      cat.textContent = act.name; // too short for two lines: show the name only
      name.hidden = true;
    } else {
      cat.textContent = act.category;
      name.textContent = act.name;
      name.hidden = false;
    }
    el.style.height = `${Math.max(act.duration, 0)}px`; // 1px per minute
  };

  // Right-click (or ctrl-click) edits, like the old app.
  el.addEventListener('contextmenu', async (e) => {
    e.preventDefault();
    if (opening) return;
    opening = true;
    try {
      const r = await openWorkDialog({
        title: 'Edit work',
        startTime: act.startTime,
        duration: act.duration,
        name: act.name,
        tags: act.tags,
        categoryId: act.categoryId,
      });
      if (!r) return;
      const updated = await api.put(`/api/activities/${act.id}`, {
        startTime: r.startTime, duration: r.duration, name: r.name, tags: r.tags, categoryId: r.categoryId,
      });
      Object.assign(act, updated); // updated in place: no re-sort until the next load
      refresh();
    } finally {
      opening = false;
    }
  });

  refresh();
  return el;
}

// ---- plan / do work ---------------------------------------------------------

let opening = false; // guards against opening two dialogs at once

async function planWork() {
  if (!dateInput.value || opening) return;
  opening = true;
  let again = false;
  try {
    const { time } = await api.get(`/api/earliest?date=${dateInput.value}`);
    const r = await openWorkDialog({ title: 'Plan work', startTime: time, duration: 60, allowNext: true });
    if (!r) return;
    const created = await api.post('/api/activities', {
      kind: 'planned', date: dateInput.value,
      startTime: r.startTime, duration: r.duration, name: r.name, tags: r.tags, categoryId: r.categoryId,
    });
    panes.planned.appendChild(renderEntry(created));
    again = r.action === 'next';
  } finally {
    opening = false;
  }
  if (again) await planWork();
}

async function doWork() {
  if (!dateInput.value || opening) return;
  opening = true;
  try {
    // Next 15-minute boundary strictly after now, as in the old app.
    const now = new Date();
    const startMins = now.getHours() * 60 + 15 * (Math.floor(now.getMinutes() / 15) + 1);
    const r = await openWorkDialog({ title: 'Do work', startTime: fmtTime(startMins), duration: 60 });
    if (!r) return;
    const created = await api.post('/api/activities', {
      kind: 'done', date: dateInput.value,
      startTime: r.startTime, duration: r.duration, name: r.name, tags: r.tags, categoryId: r.categoryId,
    });
    panes.done.appendChild(renderEntry(created));
  } finally {
    opening = false;
  }
}

$('#planBtn').addEventListener('click', planWork);
$('#doBtn').addEventListener('click', doWork);

document.addEventListener('keydown', (e) => {
  if (e.metaKey || e.ctrlKey || e.altKey || e.repeat) return;
  if (document.querySelector('dialog[open]')) return;
  const key = e.key.toLowerCase();
  if (key === 'p') { e.preventDefault(); planWork(); }
  else if (key === 'd') { e.preventDefault(); doWork(); }
});

// ---- work dialog ------------------------------------------------------------

const workDialog = $('#workDialog');
const workForm = $('#workForm');
const startInput = $('#startTime');
const durationInput = $('#duration');
const nameInput = $('#name');
const tagsInput = $('#tags');
const categorySel = $('#category');
const nextBtn = $('#nextBtn');
const okBtn = $('#okBtn');

const form = { start: 480, duration: 60, result: null };
let categories = [];

function syncDialog() {
  startInput.value = fmtTime(form.start);
  durationInput.value = String(form.duration);
  const valid = nameInput.value !== '' && categorySel.value !== '';
  okBtn.disabled = !valid;
  nextBtn.disabled = !valid;
}

function fillCategories(selectedId) {
  const placeholder = new Option('Category...', '', true, true);
  placeholder.disabled = true;
  const options = [...categories]
    .sort((a, b) => byName(a.name, b.name))
    .map((c) => new Option(c.name, String(c.id)));
  categorySel.replaceChildren(placeholder, ...options);
  categorySel.value = selectedId ? String(selectedId) : '';
}

// Up/Down step the (read-only) start time and duration by 15 minutes.
startInput.addEventListener('keydown', (e) => {
  if (e.key === 'ArrowUp') { form.start = mod(form.start + 15, 1440); }
  else if (e.key === 'ArrowDown') { form.start = mod(form.start - 15, 1440); }
  else { return; }
  e.preventDefault();
  syncDialog();
});
durationInput.addEventListener('keydown', (e) => {
  if (e.key === 'ArrowUp') { form.duration += 15; }
  else if (e.key === 'ArrowDown') { form.duration -= 15; }
  else { return; }
  e.preventDefault();
  syncDialog();
});
nameInput.addEventListener('input', syncDialog);
categorySel.addEventListener('change', syncDialog);

function finish(action) {
  form.result = {
    action,
    startTime: fmtTime(form.start),
    duration: form.duration,
    name: nameInput.value,
    tags: tagsInput.value,
    categoryId: Number(categorySel.value),
  };
  workDialog.close();
}

workForm.addEventListener('submit', (e) => {
  e.preventDefault();
  if (!okBtn.disabled) finish('ok');
});
nextBtn.addEventListener('click', () => { if (!nextBtn.disabled) finish('next'); });
$('#cancelBtn').addEventListener('click', () => workDialog.close());

async function openWorkDialog({ title, startTime, duration, name = '', tags = '', categoryId = null, allowNext = false }) {
  categories = await api.get('/api/categories');
  $('#workTitle').textContent = title;
  form.start = parseTime(startTime);
  form.duration = duration;
  form.result = null;
  nameInput.value = name;
  tagsInput.value = tags;
  fillCategories(categoryId);
  nextBtn.hidden = !allowNext;
  syncDialog();
  return new Promise((resolve) => {
    workDialog.addEventListener('close', () => resolve(form.result), { once: true });
    workDialog.showModal();
    startInput.focus();
  });
}

// ---- add category -----------------------------------------------------------

const catDialog = $('#catDialog');
const catName = $('#catName');
const catOk = $('#catOk');
let catResult = null;

catName.addEventListener('input', () => { catOk.disabled = catName.value === ''; });
$('#catForm').addEventListener('submit', (e) => {
  e.preventDefault();
  if (catName.value === '') return;
  catResult = catName.value;
  catDialog.close();
});
$('#catCancel').addEventListener('click', () => catDialog.close());

function askCategoryName() {
  catName.value = '';
  catOk.disabled = true;
  catResult = null;
  return new Promise((resolve) => {
    catDialog.addEventListener('close', () => resolve(catResult), { once: true });
    catDialog.showModal();
    catName.focus();
  });
}

$('#addCategory').addEventListener('click', async () => {
  const name = await askCategoryName();
  if (name === null) return;
  const previous = categorySel.value;
  await api.post('/api/categories', { name });
  categories = await api.get('/api/categories');
  fillCategories(previous || null);
  syncDialog();
});

// ---- report -----------------------------------------------------------------

const reportDialog = $('#reportDialog');
const repFrom = $('#repFrom');
const repTo = $('#repTo');
const chartHost = $('#chart');
const tip = $('#tip');
const SVG_NS = 'http://www.w3.org/2000/svg';
let lastReport = null;

function mondayOfThisWeek() {
  const d = new Date();
  d.setDate(d.getDate() - mod(d.getDay() + 6, 7));
  return d;
}

$('#reportBtn').addEventListener('click', () => {
  repFrom.value = fmtDate(mondayOfThisWeek());
  repTo.value = fmtDate(new Date());
  lastReport = null;
  chartHost.replaceChildren();
  reportDialog.showModal();
  loadReport();
});
$('#repClose').addEventListener('click', () => reportDialog.close());
repFrom.addEventListener('change', loadReport);
repTo.addEventListener('change', loadReport);
window.addEventListener('resize', () => { if (reportDialog.open && lastReport) drawChart(lastReport); });

async function loadReport() {
  const from = repFrom.value;
  const to = repTo.value;
  if (!from || !to || from > to) return;
  const data = await api.get(`/api/report?from=${from}&to=${to}`);
  if (from !== repFrom.value || to !== repTo.value) return;
  lastReport = data;
  drawChart(data);
}

function svgEl(tag, attrs = {}, text) {
  const n = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  if (text !== undefined) n.textContent = text;
  return n;
}

function niceStep(range, target = 6) {
  const raw = range / target;
  const pow = 10 ** Math.floor(Math.log10(raw));
  const f = raw / pow;
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * pow;
}

const fmtHours = (h) => h.toFixed(2).replace(/\.?0+$/, '');

function attachTip(node, bar) {
  let showTimer;
  let hideTimer;
  const place = (e) => {
    tip.style.left = `${e.clientX + 12}px`;
    tip.style.top = `${e.clientY + 12}px`;
  };
  node.addEventListener('mouseenter', (e) => {
    showTimer = setTimeout(() => {
      tip.textContent = Object.entries(bar.tags)
        .sort(([a], [b]) => byName(a, b))
        .map(([tag, h]) => `${tag}: ${fmtHours(h)} h`)
        .join('\n');
      place(e);
      tip.hidden = false;
      hideTimer = setTimeout(() => { tip.hidden = true; }, 20000);
    }, 250);
  });
  node.addEventListener('mousemove', (e) => { if (!tip.hidden) place(e); });
  node.addEventListener('mouseleave', () => {
    clearTimeout(showTimer);
    clearTimeout(hideTimer);
    tip.hidden = true;
  });
}

function drawChart(data) {
  tip.hidden = true;
  chartHost.replaceChildren();

  const W = Math.max(chartHost.clientWidth, 400);
  const H = 340;
  const m = { top: 40, right: 20, bottom: 78, left: 64 };
  const pw = W - m.left - m.right;
  const ph = H - m.top - m.bottom;

  const series = [
    { name: 'Planned', color: 'var(--planned)', bars: new Map(data.planned.map((b) => [b.category, b])) },
    { name: 'Actual', color: 'var(--done)', bars: new Map(data.done.map((b) => [b.category, b])) },
  ];
  const cats = [...new Set([...data.planned, ...data.done].map((b) => b.category))].sort(byName);

  const maxHours = Math.max(0, ...data.planned.map((b) => b.hours), ...data.done.map((b) => b.hours));
  const step = maxHours > 0 ? niceStep(maxHours) : 1;
  const yMax = Math.max(step, Math.ceil(maxHours / step) * step);
  const y = (h) => m.top + ph - (h / yMax) * ph;

  const svg = svgEl('svg', { viewBox: `0 0 ${W} ${H}`, height: H, role: 'img' });
  svg.appendChild(svgEl('text', {
    x: W / 2, y: 20, 'text-anchor': 'middle', 'font-size': 15, fill: 'currentColor',
  }, 'Time per category (planned vs actual)'));

  // Y axis: gridlines, tick labels, title.
  for (let v = 0; v <= yMax + step / 1000; v += step) {
    svg.appendChild(svgEl('line', { x1: m.left, x2: m.left + pw, y1: y(v), y2: y(v), style: 'stroke: var(--grid)' }));
    svg.appendChild(svgEl('text', {
      x: m.left - 8, y: y(v) + 4, 'text-anchor': 'end', fill: 'currentColor',
    }, fmtHours(v)));
  }
  svg.appendChild(svgEl('text', {
    transform: `translate(14 ${m.top + ph / 2}) rotate(-90)`, 'text-anchor': 'middle', fill: 'currentColor',
  }, 'Time (hours)'));

  // Bars, grouped by category: Planned then Actual.
  const band = pw / Math.max(cats.length, 1);
  const gap = Math.min(40, band * 0.4);
  const barW = Math.max((band - gap) / 2, 2);
  cats.forEach((cat, i) => {
    const x0 = m.left + i * band + gap / 2;
    series.forEach((s, j) => {
      const b = s.bars.get(cat);
      if (!b) return;
      const top = y(Math.max(b.hours, 0));
      const rect = svgEl('rect', {
        x: x0 + j * barW, y: top, width: barW, height: Math.max(y(0) - top, 0), style: `fill: ${s.color}`,
      });
      attachTip(rect, b);
      svg.appendChild(rect);
    });
    svg.appendChild(svgEl('text', {
      x: m.left + i * band + band / 2, y: m.top + ph + 18, 'text-anchor': 'middle', fill: 'currentColor',
    }, cat));
  });
  svg.appendChild(svgEl('line', { x1: m.left, x2: m.left + pw, y1: y(0), y2: y(0), stroke: 'currentColor' }));
  svg.appendChild(svgEl('text', {
    x: m.left + pw / 2, y: m.top + ph + 40, 'text-anchor': 'middle', fill: 'currentColor',
  }, 'Category'));

  // Legend.
  let lx = W / 2 - 70;
  for (const s of series) {
    svg.appendChild(svgEl('rect', { x: lx, y: H - 22, width: 10, height: 10, style: `fill: ${s.color}` }));
    svg.appendChild(svgEl('text', { x: lx + 15, y: H - 13, fill: 'currentColor' }, s.name));
    lx += 80;
  }

  chartHost.appendChild(svg);
}

// ---- go ---------------------------------------------------------------------

loadWork();
