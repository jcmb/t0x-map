(() => {
  const base = (() => {
    const p = location.pathname.replace(/\/index\.html$/, "");
    if (p === "/" || p === "") return "";
    return p.replace(/\/$/, "");
  })();
  const api = (path) => `${base}${path}`;

  const groupEl = document.getElementById("group");
  const receiverEl = document.getElementById("receiver");
  const tzEl = document.getElementById("tz");
  const fromEl = document.getElementById("from");
  const toEl = document.getElementById("to");
  const statusEl = document.getElementById("status");
  const timelineEl = document.getElementById("timeline");
  const dayTableEl = document.getElementById("day-table");
  const selectionListEl = document.getElementById("selection-list");
  const downloadBtn = document.getElementById("download-selected");
  const selectAllBtn = document.getElementById("select-all");
  const clearBtn = document.getElementById("clear-selection");
  const tabTimeline = document.querySelector('.view-tab[data-view="timeline"]');
  const tabTable = document.querySelector('.view-tab[data-view="table"]');

  let meta = { groups: [], receivers: {} };
  let view = "map";
  let allFiles = [];
  let lastFeatures = [];
  const selected = new Set();
  const fileById = new Map();
  const expandedDays = new Set();

  // Timeline zoom window (ms since epoch), relative to current data extent.
  let timelineDataKey = "";
  let timelineDataMin = 0;
  let timelineDataMax = 0;
  let timelineViewMin = 0;
  let timelineViewMax = 0;
  let timelinePlotW = 720;
  let timelineLabelW = 140;
  let timelinePanning = false;
  let timelinePanLastX = 0;
  let timelinePanRaf = 0;

  const map = L.map("map", { worldCopyJump: true }).setView([20, 0], 2);
  L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: "&copy; OpenStreetMap",
  }).addTo(map);
  const layer = L.layerGroup().addTo(map);

  function tzMode() {
    return tzEl.value === "local" ? "local" : "utc";
  }

  function pad2(n) {
    return String(n).padStart(2, "0");
  }

  function todayYMD() {
    const d = new Date();
    if (tzMode() === "utc") return d.toISOString().slice(0, 10);
    return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
  }

  function addDaysYMD(ymd, days) {
    const [y, m, d] = ymd.split("-").map(Number);
    if (tzMode() === "utc") {
      const dt = new Date(Date.UTC(y, m - 1, d));
      dt.setUTCDate(dt.getUTCDate() + days);
      return dt.toISOString().slice(0, 10);
    }
    const dt = new Date(y, m - 1, d);
    dt.setDate(dt.getDate() + days);
    return `${dt.getFullYear()}-${pad2(dt.getMonth() + 1)}-${pad2(dt.getDate())}`;
  }

  function setDefaultDates() {
    const today = todayYMD();
    fromEl.value = addDaysYMD(today, -7);
    toEl.value = today;
  }

  function clearDates() {
    fromEl.value = "";
    toEl.value = "";
  }

  function parseISO(iso) {
    if (!iso) return null;
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? null : d;
  }

  /** Calendar day key for grouping, in the selected timezone. */
  function dayKeyFromISO(iso) {
    const d = parseISO(iso);
    if (!d) return "unknown";
    if (tzMode() === "utc") return d.toISOString().slice(0, 10);
    return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
  }

  function formatTimeOnly(iso) {
    const d = parseISO(iso);
    if (!d) return "—";
    if (tzMode() === "utc") {
      return `${pad2(d.getUTCHours())}:${pad2(d.getUTCMinutes())}:${pad2(d.getUTCSeconds())}`;
    }
    return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
  }

  function formatDateTime(iso) {
    const d = parseISO(iso);
    if (!d) return "—";
    if (tzMode() === "utc") {
      return d.toISOString().replace("T", " ").replace(/\.\d+Z$/, " Z");
    }
    return d.toLocaleString(undefined, { hour12: false });
  }

  function formatAxisTick(ms) {
    const d = new Date(ms);
    if (tzMode() === "utc") {
      return `${d.toISOString().slice(5, 10)} ${pad2(d.getUTCHours())}:${pad2(d.getUTCMinutes())}`;
    }
    return `${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
  }

  /** Nice minute-aligned step for the visible span (always lands on :00 seconds). */
  function timelineTickStep(spanMs) {
    const m = 60e3;
    const h = 3600e3;
    const d = 86400e3;
    const steps = [
      m,
      2 * m,
      5 * m,
      10 * m,
      15 * m,
      30 * m,
      h,
      2 * h,
      3 * h,
      6 * h,
      12 * h,
      d,
      2 * d,
      7 * d,
      14 * d,
      30 * d,
    ];
    const ideal = spanMs / 8;
    for (const s of steps) {
      if (s >= ideal) return s;
    }
    return steps[steps.length - 1];
  }

  /** First tick at or after viewMin, snapped to a :00-minute grid (or local/UTC midnight for day steps). */
  function firstTimelineTick(viewMin, step) {
    const day = 86400e3;
    if (step >= day) {
      const d = new Date(viewMin);
      if (tzMode() === "utc") {
        d.setUTCHours(0, 0, 0, 0);
        if (d.getTime() < viewMin) d.setUTCDate(d.getUTCDate() + 1);
      } else {
        d.setHours(0, 0, 0, 0);
        if (d.getTime() < viewMin) d.setDate(d.getDate() + 1);
      }
      // Align to multi-day step from epoch days when step > 1 day.
      if (step > day) {
        const days = Math.floor(d.getTime() / day);
        const stepDays = Math.round(step / day);
        const aligned = Math.ceil(days / stepDays) * stepDays;
        return aligned * day;
      }
      return d.getTime();
    }
    // Minute/hour steps divide evenly into the Unix timeline → always :00 minutes.
    return Math.ceil(viewMin / step) * step;
  }

  function timelineTicks(viewMin, viewMax) {
    const span = Math.max(1, viewMax - viewMin);
    const step = timelineTickStep(span);
    const ticks = [];
    let t = firstTimelineTick(viewMin, step);
    const maxTicks = 24;
    while (t <= viewMax && ticks.length < maxTicks) {
      ticks.push(t);
      t += step;
    }
    return ticks;
  }

  /** Convert a date-input YMD into API from/to (UTC day or RFC3339 for local). */
  function apiFromBound(ymd) {
    if (!ymd) return "";
    if (tzMode() === "utc") return ymd;
    const [y, m, d] = ymd.split("-").map(Number);
    return new Date(y, m - 1, d, 0, 0, 0, 0).toISOString();
  }

  function apiToBound(ymd) {
    if (!ymd) return "";
    if (tzMode() === "utc") return ymd;
    const [y, m, d] = ymd.split("-").map(Number);
    return new Date(y, m - 1, d, 23, 59, 59, 999).toISOString();
  }

  function formatSize(n) {
    if (n == null) return "";
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  }

  function fileDayKey(f) {
    return dayKeyFromISO(f.start_time || f.end_time);
  }

  function groupByDay(files) {
    const days = new Map();
    for (const f of files) {
      const day = fileDayKey(f);
      if (!days.has(day)) days.set(day, []);
      days.get(day).push(f);
    }
    // Newest day first.
    return [...days.entries()].sort((a, b) => {
      if (a[0] === "unknown") return 1;
      if (b[0] === "unknown") return -1;
      return b[0].localeCompare(a[0]);
    });
  }

  function startOfDayYMD(ymd) {
    const [y, m, d] = ymd.split("-").map(Number);
    if (tzMode() === "utc") return Date.UTC(y, m - 1, d, 0, 0, 0, 0);
    return new Date(y, m - 1, d, 0, 0, 0, 0).getTime();
  }

  function endOfDayYMD(ymd) {
    const [y, m, d] = ymd.split("-").map(Number);
    if (tzMode() === "utc") return Date.UTC(y, m - 1, d, 23, 59, 59, 999);
    return new Date(y, m - 1, d, 23, 59, 59, 999).getTime();
  }

  function endOfDisplayDay(ms) {
    const d = new Date(ms);
    if (tzMode() === "utc") {
      d.setUTCHours(23, 59, 59, 999);
    } else {
      d.setHours(23, 59, 59, 999);
    }
    return d.getTime();
  }

  function groupReceivers(g) {
    return (meta.receivers[g] || []).filter((r) => r && String(r).trim());
  }

  function fillGroups() {
    const current = groupEl.value;
    groupEl.innerHTML = '<option value="">All groups</option>';
    for (const g of meta.groups || []) {
      if (!groupReceivers(g).length) continue;
      const opt = document.createElement("option");
      opt.value = g;
      opt.textContent = g;
      groupEl.appendChild(opt);
    }
    if ([...groupEl.options].some((o) => o.value === current)) groupEl.value = current;
    fillReceivers();
  }

  function fillReceivers() {
    const g = groupEl.value;
    const current = receiverEl.value;
    receiverEl.innerHTML = '<option value="">All receivers</option>';
    const list = g
      ? groupReceivers(g)
      : Object.keys(meta.receivers || {})
          .filter((name) => groupReceivers(name).length)
          .flatMap((name) => groupReceivers(name));
    const uniq = [...new Set(list)].sort();
    for (const r of uniq) {
      const opt = document.createElement("option");
      opt.value = r;
      opt.textContent = r;
      receiverEl.appendChild(opt);
    }
    if (uniq.includes(current)) receiverEl.value = current;
  }

  async function goToTimelineForReceiver(group, receiver) {
    if (!group || !receiver) return;
    groupEl.value = group;
    fillReceivers();
    receiverEl.value = receiver;
    updateTabAvailability();
    await loadFiles();
    setView("timeline");
  }

  async function goToTableForReceiver(receiver) {
    if (!receiver || !groupEl.value) return;
    fillReceivers();
    receiverEl.value = receiver;
    updateTabAvailability();
    await loadFiles();
    setView("table");
  }

  function updateTabAvailability() {
    const hasGroup = !!groupEl.value;
    const hasReceiver = !!receiverEl.value;
    tabTimeline.disabled = !hasGroup;
    tabTimeline.title = hasGroup ? "" : "Select a group first";
    tabTable.disabled = !hasReceiver;
    tabTable.title = hasReceiver ? "" : "Select a receiver first";
    if (view === "timeline" && !hasGroup) setView("map");
    if (view === "table" && !hasReceiver) setView("map");
  }

  function setView(next) {
    if (next === "timeline" && !groupEl.value) return;
    if (next === "table" && !receiverEl.value) return;
    view = next;
    document.querySelectorAll(".view-tab").forEach((btn) => {
      const on = btn.dataset.view === view;
      btn.classList.toggle("active", on);
      btn.setAttribute("aria-selected", on ? "true" : "false");
    });
    document.querySelectorAll(".view-pane").forEach((pane) => {
      pane.classList.toggle("active", pane.id === `view-${view}`);
    });
    if (view === "map") setTimeout(() => map.invalidateSize(), 50);
    if (view === "timeline") renderTimelineChart();
    if (view === "table") renderDayTable();
  }

  function syncSelectionUI() {
    const n = selected.size;
    downloadBtn.disabled = n === 0;
    clearBtn.disabled = n === 0;
    selectAllBtn.disabled = allFiles.length === 0;
    downloadBtn.textContent =
      n === 0 ? "Download selected" : `Download selected (${n})`;

    selectionListEl.innerHTML = "";
    if (!n) {
      selectionListEl.innerHTML = '<p class="empty">No files selected</p>';
    } else {
      for (const id of selected) {
        const f = fileById.get(id);
        const row = document.createElement("div");
        row.className = "sel-row";
        const span = document.createElement("span");
        span.textContent = f ? f.filename : `#${id}`;
        const btn = document.createElement("button");
        btn.type = "button";
        btn.textContent = "remove";
        btn.addEventListener("click", () => {
          selected.delete(id);
          syncSelectionUI();
          if (view === "timeline") renderTimelineChart();
          if (view === "table") renderDayTable();
        });
        row.append(span, btn);
        selectionListEl.appendChild(row);
      }
    }
    if (view === "timeline") {
      timelineEl.querySelectorAll(".bar").forEach((el) => {
        el.classList.toggle("selected", selected.has(Number(el.dataset.id)));
      });
    }
  }

  function timelineKey() {
    const first = allFiles[0];
    const last = allFiles[allFiles.length - 1];
    return [
      groupEl.value,
      fromEl.value,
      toEl.value,
      tzMode(),
      allFiles.length,
      first?.id,
      last?.id,
    ].join("|");
  }

  function resetTimelineZoom(dataMin, dataMax) {
    timelineDataMin = dataMin;
    timelineDataMax = dataMax;
    timelineViewMin = dataMin;
    timelineViewMax = dataMax;
  }

  function clampTimelineView() {
    const full = timelineDataMax - timelineDataMin;
    if (full <= 0) return;
    const minSpan = Math.min(60e3, full); // 1 minute, or full span if shorter
    let span = timelineViewMax - timelineViewMin;
    span = Math.min(full, Math.max(minSpan, span));
    if (timelineViewMin < timelineDataMin) {
      timelineViewMin = timelineDataMin;
      timelineViewMax = timelineViewMin + span;
    }
    if (timelineViewMax > timelineDataMax) {
      timelineViewMax = timelineDataMax;
      timelineViewMin = timelineViewMax - span;
    }
    if (timelineViewMin < timelineDataMin) {
      timelineViewMin = timelineDataMin;
      timelineViewMax = timelineDataMin + span;
    }
  }

  function zoomTimelineAt(frac, factor) {
    frac = Math.min(1, Math.max(0, frac));
    const anchor = timelineViewMin + frac * (timelineViewMax - timelineViewMin);
    let span = (timelineViewMax - timelineViewMin) * factor;
    const full = timelineDataMax - timelineDataMin;
    const minSpan = Math.min(60e3, full);
    span = Math.min(full, Math.max(minSpan, span));
    timelineViewMin = anchor - frac * span;
    timelineViewMax = timelineViewMin + span;
    clampTimelineView();
    renderTimelineChart();
  }

  function panTimelineByPixels(dx) {
    const span = timelineViewMax - timelineViewMin;
    if (timelinePlotW <= 0 || span <= 0) return;
    const dms = (-dx / timelinePlotW) * span;
    timelineViewMin += dms;
    timelineViewMax += dms;
    clampTimelineView();
    if (timelinePanRaf) cancelAnimationFrame(timelinePanRaf);
    timelinePanRaf = requestAnimationFrame(() => {
      timelinePanRaf = 0;
      renderTimelineChart();
    });
  }

  function renderTimelineChart() {
    timelineEl.innerHTML = "";
    if (!groupEl.value) {
      timelineEl.innerHTML = '<p class="hint">Select a group to use Timeline view.</p>';
      return;
    }
    const files = allFiles.filter((f) => f.start_time && f.end_time);
    if (!files.length) {
      timelineEl.innerHTML =
        '<p class="hint">No timed files in this range for the group.</p>';
      return;
    }

    const receivers = [...new Set(files.map((f) => f.receiver))].sort();
    let dataMin = Infinity;
    let dataMax = -Infinity;
    for (const f of files) {
      const a = parseISO(f.start_time)?.getTime();
      const b = parseISO(f.end_time)?.getTime();
      if (a != null) dataMin = Math.min(dataMin, a);
      if (b != null) dataMax = Math.max(dataMax, b);
      // Zero-length / missing end: still count the start instant.
      if (a != null && (b == null || b < a)) dataMax = Math.max(dataMax, a);
    }
    if (!Number.isFinite(dataMin) || !Number.isFinite(dataMax)) {
      dataMin = Date.now();
      dataMax = dataMin + 3600e3;
    }
    if (dataMax <= dataMin) dataMax = dataMin + 60e3;

    // Tight fit to data on the left; open the right to end-of-day of the last
    // data day so the axis is not truncated at that day's start (00:00).
    dataMax = endOfDisplayDay(dataMax);

    // Keep inside the inclusive date filter when set (to = end of that day).
    if (fromEl.value) {
      const fromMs = startOfDayYMD(fromEl.value);
      if (Number.isFinite(fromMs) && dataMin < fromMs) dataMin = fromMs;
    }
    if (toEl.value) {
      const toMs = endOfDayYMD(toEl.value);
      if (Number.isFinite(toMs) && dataMax > toMs) dataMax = toMs;
    }
    if (dataMax <= dataMin) dataMax = dataMin + 60e3;

    // Small pad on the left only (right already at end-of-day).
    const pad = Math.min(30 * 60e3, Math.max(60e3, (dataMax - dataMin) * 0.01));
    dataMin -= pad;

    const key = timelineKey();
    if (key !== timelineDataKey) {
      timelineDataKey = key;
      resetTimelineZoom(dataMin, dataMax);
    } else {
      timelineDataMin = dataMin;
      timelineDataMax = dataMax;
      clampTimelineView();
    }

    const viewMin = timelineViewMin;
    const viewMax = timelineViewMax;
    const viewSpan = Math.max(1, viewMax - viewMin);

    const labelW = 140;
    const topH = 28;
    const rowH = 28;
    // Fit plot to the visible pane (no forced min-width horizontal scroll).
    const plotW = Math.max(320, (timelineEl.clientWidth || 320) - 8);
    timelineLabelW = labelW;
    timelinePlotW = plotW;
    const width = labelW + plotW;
    const height = topH + receivers.length * rowH + 8;

    const xOf = (ms) => labelW + ((ms - viewMin) / viewSpan) * plotW;

    const svgNS = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(svgNS, "svg");
    svg.setAttribute("width", String(width));
    svg.setAttribute("height", String(height));
    svg.setAttribute("viewBox", `0 0 ${width} ${height}`);

    const defs = document.createElementNS(svgNS, "defs");
    const clip = document.createElementNS(svgNS, "clipPath");
    clip.setAttribute("id", "tl-plot-clip");
    const clipRect = document.createElementNS(svgNS, "rect");
    clipRect.setAttribute("x", String(labelW));
    clipRect.setAttribute("y", String(topH));
    clipRect.setAttribute("width", String(plotW));
    clipRect.setAttribute("height", String(height - topH));
    clip.appendChild(clipRect);
    defs.appendChild(clip);
    svg.appendChild(defs);

    receivers.forEach((recv, i) => {
      const y = topH + i * rowH;
      const rect = document.createElementNS(svgNS, "rect");
      rect.setAttribute("class", "lane-bg");
      rect.setAttribute("x", "0");
      rect.setAttribute("y", String(y));
      rect.setAttribute("width", String(width));
      rect.setAttribute("height", String(rowH));
      svg.appendChild(rect);

      const text = document.createElementNS(svgNS, "text");
      text.setAttribute("class", "lane-label");
      text.setAttribute("x", "8");
      text.setAttribute("y", String(y + rowH / 2 + 4));
      text.textContent = recv.length > 18 ? recv.slice(0, 17) + "…" : recv;
      text.setAttribute("title", `${recv} — open Table`);
      text.addEventListener("pointerdown", (e) => e.stopPropagation());
      text.addEventListener("click", (e) => {
        e.stopPropagation();
        goToTableForReceiver(recv).catch((err) => {
          statusEl.textContent = String(err);
        });
      });
      svg.appendChild(text);
    });

    for (const ms of timelineTicks(viewMin, viewMax)) {
      const x = xOf(ms);
      if (x < labelW - 1 || x > labelW + plotW + 1) continue;
      const line = document.createElementNS(svgNS, "line");
      line.setAttribute("class", "grid");
      line.setAttribute("x1", String(x));
      line.setAttribute("x2", String(x));
      line.setAttribute("y1", String(topH));
      line.setAttribute("y2", String(height));
      svg.appendChild(line);

      const lab = document.createElementNS(svgNS, "text");
      lab.setAttribute("class", "axis-label");
      lab.setAttribute("x", String(x));
      lab.setAttribute("y", "16");
      lab.setAttribute("text-anchor", "middle");
      lab.textContent = formatAxisTick(ms);
      svg.appendChild(lab);
    }

    const barsG = document.createElementNS(svgNS, "g");
    barsG.setAttribute("clip-path", "url(#tl-plot-clip)");

    const rowIndex = new Map(receivers.map((r, i) => [r, i]));
    for (const f of files) {
      const a = parseISO(f.start_time)?.getTime();
      const b = parseISO(f.end_time)?.getTime();
      if (a == null || b == null) continue;
      if (b < viewMin || a > viewMax) continue;
      const i = rowIndex.get(f.receiver);
      if (i == null) continue;
      let x1 = xOf(a);
      let x2 = xOf(Math.max(b, a));
      if (x2 - x1 < 3) x2 = x1 + 3;
      const y = topH + i * rowH + 6;
      const rect = document.createElementNS(svgNS, "rect");
      rect.setAttribute("class", "bar" + (selected.has(f.id) ? " selected" : ""));
      rect.setAttribute("x", String(x1));
      rect.setAttribute("y", String(y));
      rect.setAttribute("width", String(x2 - x1));
      rect.setAttribute("height", String(rowH - 12));
      rect.setAttribute("rx", "3");
      rect.dataset.id = String(f.id);
      rect.setAttribute(
        "title",
        `${f.filename}\n${formatDateTime(f.start_time)} → ${formatDateTime(f.end_time)}`
      );
      rect.addEventListener("click", (e) => {
        e.stopPropagation();
        if (selected.has(f.id)) selected.delete(f.id);
        else selected.add(f.id);
        syncSelectionUI();
        renderTimelineChart();
      });
      barsG.appendChild(rect);
    }
    svg.appendChild(barsG);

    const frame = document.createElementNS(svgNS, "rect");
    frame.setAttribute("class", "plot-frame");
    frame.setAttribute("x", String(labelW));
    frame.setAttribute("y", String(topH));
    frame.setAttribute("width", String(plotW));
    frame.setAttribute("height", String(height - topH));
    svg.appendChild(frame);

    timelineEl.appendChild(svg);
  }

  function rateLinks(fileId, forDay) {
    const wrap = document.createElement("div");
    wrap.className = "dl-links";
    if (forDay) {
      const orig = document.createElement("a");
      orig.href = "#";
      orig.textContent = "Original";
      const s1 = document.createElement("span");
      s1.className = "soon";
      s1.textContent = "1 second";
      s1.title = "Coming later";
      const s30 = document.createElement("span");
      s30.className = "soon";
      s30.textContent = "30 seconds";
      s30.title = "Coming later";
      wrap.append(orig, s1, s30);
      return wrap;
    }
    const orig = document.createElement("a");
    orig.href = api("/api/download/" + fileId);
    orig.textContent = "Original";
    const s1 = document.createElement("span");
    s1.className = "soon";
    s1.textContent = "1 second";
    s1.title = "Coming later";
    const s30 = document.createElement("span");
    s30.className = "soon";
    s30.textContent = "30 seconds";
    s30.title = "Coming later";
    wrap.append(orig, s1, s30);
    return wrap;
  }

  function daySummaryTimes(dayFiles) {
    let minStart = null;
    let maxEnd = null;
    for (const f of dayFiles) {
      if (f.start_time && (minStart === null || f.start_time < minStart)) minStart = f.start_time;
      if (f.end_time && (maxEnd === null || f.end_time > maxEnd)) maxEnd = f.end_time;
    }
    return { start: minStart, end: maxEnd };
  }

  function renderDayTable() {
    dayTableEl.innerHTML = "";
    if (!receiverEl.value) {
      dayTableEl.innerHTML =
        '<p class="table-hint">Select a receiver to open the Table view.</p>';
      return;
    }
    const files = allFiles.filter((f) => f.receiver === receiverEl.value);
    if (!files.length) {
      dayTableEl.innerHTML =
        '<p class="table-hint">No files for this receiver in the selected date range.</p>';
      return;
    }

    const byDay = groupByDay(files);
    const table = document.createElement("table");
    const tzNote = tzMode() === "utc" ? "UTC" : "local";
    table.innerHTML = `
      <thead>
        <tr>
          <th></th>
          <th>Day (${tzNote})</th>
          <th>Start</th>
          <th>End</th>
          <th>Files</th>
          <th>Download</th>
        </tr>
      </thead>
    `;
    const tbody = document.createElement("tbody");

    for (const [day, dayFiles] of byDay) {
      const times = daySummaryTimes(dayFiles);
      const open = expandedDays.has(day);

      const sum = document.createElement("tr");
      sum.className = "day-summary";

      const tdToggle = document.createElement("td");
      const toggle = document.createElement("span");
      toggle.className = "toggle";
      toggle.textContent = open ? "▾" : "▸";
      tdToggle.appendChild(toggle);

      const tdDay = document.createElement("td");
      tdDay.textContent = day;
      const tdStart = document.createElement("td");
      tdStart.textContent = formatTimeOnly(times.start);
      const tdEnd = document.createElement("td");
      tdEnd.textContent = formatTimeOnly(times.end);
      const tdCount = document.createElement("td");
      tdCount.textContent = String(dayFiles.length);
      const tdDl = document.createElement("td");
      const dayLinks = rateLinks(null, true);
      dayLinks.querySelector("a").addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        for (const f of dayFiles) selected.add(f.id);
        syncSelectionUI();
        statusEl.textContent = `Selected ${dayFiles.length} file(s) for ${day}. Use Download selected.`;
      });
      tdDl.appendChild(dayLinks);

      sum.append(tdToggle, tdDay, tdStart, tdEnd, tdCount, tdDl);
      sum.addEventListener("click", (e) => {
        if (e.target.closest(".dl-links")) return;
        if (expandedDays.has(day)) expandedDays.delete(day);
        else expandedDays.add(day);
        renderDayTable();
      });
      tbody.appendChild(sum);

      const ordered = [...dayFiles].sort((a, b) => {
        const ta = String(a.start_time || "");
        const tb = String(b.start_time || "");
        if (ta !== tb) return tb.localeCompare(ta); // newest first
        return String(a.filename).localeCompare(String(b.filename));
      });
      for (const f of ordered) {
        const child = document.createElement("tr");
        child.className = "child-row" + (open ? "" : " hidden");
        const c0 = document.createElement("td");
        const cName = document.createElement("td");
        cName.textContent = f.filename;
        const cStart = document.createElement("td");
        cStart.textContent = formatTimeOnly(f.start_time);
        const cEnd = document.createElement("td");
        cEnd.textContent = formatTimeOnly(f.end_time);
        const cSize = document.createElement("td");
        cSize.textContent = formatSize(f.size_bytes);
        const cDl = document.createElement("td");
        cDl.appendChild(rateLinks(f.id, false));
        child.append(c0, cName, cStart, cEnd, cSize, cDl);
        tbody.appendChild(child);
      }
    }

    table.appendChild(tbody);
    dayTableEl.appendChild(table);
  }

  function renderMap() {
    layer.clearLayers();
    const bounds = [];
    for (const f of lastFeatures || []) {
      const id = f.properties?.id;
      const props = id != null ? fileById.get(id) : null;
      if (!props) continue;
      const [lon, lat] = f.geometry.coordinates;
      const marker = L.circleMarker([lat, lon], {
        radius: 7,
        color: "#1f6f4a",
        fillColor: "#3ecf8e",
        fillOpacity: 0.85,
        weight: 1,
      });
      marker.bindTooltip(`${props.group} / ${props.receiver}`, {
        direction: "top",
        opacity: 0.9,
      });
      marker.on("click", () => {
        goToTimelineForReceiver(props.group, props.receiver).catch((err) => {
          statusEl.textContent = String(err);
        });
      });
      marker.addTo(layer);
      bounds.push([lat, lon]);
    }
    if (bounds.length) map.fitBounds(bounds, { padding: [40, 40], maxZoom: 12 });
  }

  function statusText(m) {
    const parts = [];
    parts.push(tzMode() === "utc" ? "UTC" : "local");
    if (m.from || m.to) parts.push(`${m.from || "…"} → ${m.to || "…"}`);
    else parts.push("all dates");
    if (m.group) parts.push(`group ${m.group}`);
    if (m.receiver) parts.push(m.receiver);
    let msg = `${m.total} file(s) (${parts.join(", ")})`;
    if (m.without_time > 0) msg += ` · ${m.without_time} missing times`;
    if (m.on_map != null && m.without_position > 0) msg += ` · ${m.on_map} on map`;
    return msg;
  }

  async function loadMeta() {
    const res = await fetch(api("/api/meta"));
    if (!res.ok) throw new Error(`meta ${res.status}`);
    const body = await res.json();
    // Dropdowns are driven only by indexed DB rows (/api/meta).
    meta = {
      groups: Array.isArray(body.groups) ? body.groups : [],
      receivers:
        body.receivers && typeof body.receivers === "object" ? body.receivers : {},
    };
    fillGroups();
  }

  async function loadFiles() {
    const params = new URLSearchParams();
    if (groupEl.value) params.set("group", groupEl.value);
    if (receiverEl.value) params.set("receiver", receiverEl.value);
    const fromBound = apiFromBound(fromEl.value);
    const toBound = apiToBound(toEl.value);
    if (fromBound) params.set("from", fromBound);
    if (toBound) params.set("to", toBound);

    statusEl.textContent = "Loading files…";
    const res = await fetch(`${api("/api/files")}?${params}`);
    if (!res.ok) throw new Error(`files ${res.status}`);
    const gj = await res.json();

    allFiles = gj.files || [];
    lastFeatures = gj.features || [];
    fileById.clear();
    for (const f of allFiles) fileById.set(f.id, f);

    const keep = new Set(allFiles.map((f) => f.id));
    for (const id of [...selected]) {
      if (!keep.has(id)) selected.delete(id);
    }

    updateTabAvailability();
    renderMap();
    if (view === "timeline") renderTimelineChart();
    if (view === "table") renderDayTable();
    syncSelectionUI();

    statusEl.textContent = statusText(
      gj.meta || {
        total: allFiles.length,
        group: groupEl.value,
        receiver: receiverEl.value,
        from: fromEl.value,
        to: toEl.value,
        without_time: 0,
        without_position: 0,
        on_map: lastFeatures.length,
      }
    );
  }

  function reloadViews() {
    if (view === "timeline") renderTimelineChart();
    if (view === "table") renderDayTable();
    renderMap();
    syncSelectionUI();
  }

  document.querySelectorAll(".view-tab").forEach((btn) => {
    btn.addEventListener("click", () => {
      if (btn.disabled) return;
      setView(btn.dataset.view);
    });
  });

  document.getElementById("filters").addEventListener("submit", (e) => {
    e.preventDefault();
    loadFiles().catch((err) => {
      statusEl.textContent = String(err);
    });
  });

  document.getElementById("reset").addEventListener("click", () => {
    groupEl.value = "";
    receiverEl.value = "";
    tzEl.value = "utc";
    setDefaultDates();
    selected.clear();
    expandedDays.clear();
    fillReceivers();
    updateTabAvailability();
    setView("map");
    loadFiles().catch((err) => {
      statusEl.textContent = String(err);
    });
  });

  document.getElementById("all-dates").addEventListener("click", () => {
    clearDates();
    loadFiles().catch((err) => {
      statusEl.textContent = String(err);
    });
  });

  document.getElementById("last-week").addEventListener("click", () => {
    setDefaultDates();
    loadFiles().catch((err) => {
      statusEl.textContent = String(err);
    });
  });

  tzEl.addEventListener("change", () => {
    // Re-interpret date inputs in new zone for query, and refresh displays.
    loadFiles().catch((err) => {
      statusEl.textContent = String(err);
    });
  });

  groupEl.addEventListener("change", () => {
    fillReceivers();
    updateTabAvailability();
    loadFiles().catch((err) => {
      statusEl.textContent = String(err);
    });
  });

  receiverEl.addEventListener("change", () => {
    updateTabAvailability();
    loadFiles().catch((err) => {
      statusEl.textContent = String(err);
    });
  });

  selectAllBtn.addEventListener("click", () => {
    const src =
      view === "table"
        ? allFiles.filter((f) => f.receiver === receiverEl.value)
        : allFiles;
    for (const f of src) selected.add(f.id);
    syncSelectionUI();
    if (view === "timeline") renderTimelineChart();
  });

  clearBtn.addEventListener("click", () => {
    selected.clear();
    syncSelectionUI();
    if (view === "timeline") renderTimelineChart();
  });

  downloadBtn.addEventListener("click", async () => {
    const ids = [...selected];
    if (!ids.length) return;
    statusEl.textContent = "Preparing zip…";
    const res = await fetch(api("/api/download"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ids }),
    });
    if (!res.ok) {
      statusEl.textContent = `download failed (${res.status})`;
      return;
    }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "t0x-files.zip";
    a.click();
    URL.revokeObjectURL(url);
    statusEl.textContent = `Downloaded ${ids.length} file(s)`;
  });

  window.addEventListener("resize", () => {
    if (view === "timeline") renderTimelineChart();
  });

  timelineEl.addEventListener(
    "wheel",
    (e) => {
      if (view !== "timeline") return;
      const svg = timelineEl.querySelector("svg");
      if (!svg) return;
      e.preventDefault();
      const bounds = svg.getBoundingClientRect();
      const px = e.clientX - bounds.left;
      const frac = (px - timelineLabelW) / timelinePlotW;
      if (frac < 0 || frac > 1) return;
      const factor = e.deltaY < 0 ? 0.8 : 1.25;
      zoomTimelineAt(frac, factor);
    },
    { passive: false }
  );

  timelineEl.addEventListener("pointerdown", (e) => {
    if (view !== "timeline" || e.button !== 0) return;
    if (
      e.target.closest?.(".bar") ||
      e.target.classList?.contains("hint") ||
      e.target.classList?.contains("lane-label")
    ) {
      return;
    }
    if (!timelineEl.querySelector("svg")) return;
    timelinePanning = true;
    timelinePanLastX = e.clientX;
    timelineEl.classList.add("panning");
    timelineEl.setPointerCapture(e.pointerId);
  });

  timelineEl.addEventListener("pointermove", (e) => {
    if (!timelinePanning) return;
    const dx = e.clientX - timelinePanLastX;
    timelinePanLastX = e.clientX;
    if (dx !== 0) panTimelineByPixels(dx);
  });

  const endTimelinePan = (e) => {
    if (!timelinePanning) return;
    timelinePanning = false;
    timelineEl.classList.remove("panning");
    try {
      timelineEl.releasePointerCapture(e.pointerId);
    } catch (_) {
      /* ignore */
    }
  };
  timelineEl.addEventListener("pointerup", endTimelinePan);
  timelineEl.addEventListener("pointercancel", endTimelinePan);

  document.getElementById("tl-zoom-in").addEventListener("click", () => {
    if (view !== "timeline") return;
    zoomTimelineAt(0.5, 0.7);
  });
  document.getElementById("tl-zoom-out").addEventListener("click", () => {
    if (view !== "timeline") return;
    zoomTimelineAt(0.5, 1 / 0.7);
  });
  document.getElementById("tl-zoom-reset").addEventListener("click", () => {
    if (view !== "timeline") return;
    resetTimelineZoom(timelineDataMin, timelineDataMax);
    renderTimelineChart();
  });

  setDefaultDates();
  loadMeta()
    .then(() => {
      updateTabAvailability();
      setView("map");
      return loadFiles();
    })
    .catch((err) => {
      statusEl.textContent = String(err);
    });
})();
