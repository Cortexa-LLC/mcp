package knowledge

// The page that RenderHTML fills in. Kept in its own file so the renderer
// above reads as Go rather than as a wall of markup.
//
// Written without backticks so that it can live in a Go raw string literal,
// and in ES5 — no template literals, no optional chaining — so that nothing
// here needs transpiling. The binding constraint on how old a browser can be
// is not the syntax but the Pointer Events the drag and pan handlers use,
// which Safari only shipped in 13 (2019); every current browser has them.

const htmlTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>kg graph</title>
<style>
  :root {
    --bg: #ffffff;
    --panel: #f6f8fa;
    --border: #d0d7de;
    --fg: #1f2328;
    --muted: #656d76;
    --edge: #afb8c1;
    --warn-bg: #fff8c5;
    --warn-fg: #7d4e00;
    --warn-border: #d4a72c;
  }
  @media (prefers-color-scheme: dark) {
    :root:not([data-theme="light"]) {
      --bg: #0d1117;
      --panel: #161b22;
      --border: #30363d;
      --fg: #e6edf3;
      --muted: #8b949e;
      --edge: #484f58;
      --warn-bg: #2d2305;
      --warn-fg: #e3b341;
      --warn-border: #6b5415;
    }
  }
  :root[data-theme="dark"] {
    --bg: #0d1117;
    --panel: #161b22;
    --border: #30363d;
    --fg: #e6edf3;
    --muted: #8b949e;
    --edge: #484f58;
    --warn-bg: #2d2305;
    --warn-fg: #e3b341;
    --warn-border: #6b5415;
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; margin: 0; }
  body {
    background: var(--bg);
    color: var(--fg);
    font: 13px/1.5 ui-sans-serif, -apple-system, "Segoe UI", Helvetica, Arial, sans-serif;
    display: flex;
    flex-direction: column;
  }
  header {
    flex: 0 0 auto;
    padding: 10px 16px;
    border-bottom: 1px solid var(--border);
    background: var(--panel);
    display: flex;
    flex-wrap: wrap;
    gap: 8px 14px;
    align-items: center;
  }
  h1 { font-size: 14px; font-weight: 600; margin: 0; }
  #counts { color: var(--muted); font-variant-numeric: tabular-nums; }
  #warn {
    width: 100%;
    order: 9;
    padding: 6px 10px;
    border: 1px solid var(--warn-border);
    border-radius: 6px;
    background: var(--warn-bg);
    color: var(--warn-fg);
  }
  .spacer { flex: 1 1 auto; }
  input[type="search"] {
    width: 190px;
    padding: 4px 8px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: var(--fg);
    font: inherit;
  }
  button {
    padding: 4px 10px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    cursor: pointer;
  }
  button:hover { border-color: var(--muted); }
  #legend { display: flex; flex-wrap: wrap; gap: 4px 10px; align-items: center; }
  #legend label {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    cursor: pointer;
    user-select: none;
    white-space: nowrap;
  }
  #legend .swatch {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    display: inline-block;
  }
  #legend .count { color: var(--muted); font-variant-numeric: tabular-nums; }
  #legend input { margin: 0; }
  #stage { flex: 1 1 auto; position: relative; overflow: hidden; }
  svg { width: 100%; height: 100%; display: block; cursor: grab; touch-action: none; }
  svg.panning { cursor: grabbing; }
  .edge { stroke: var(--edge); stroke-width: 1; fill: none; }
  .edge.derived { stroke-dasharray: 4 3; }
  .node { cursor: pointer; }
  .node circle { stroke: var(--bg); stroke-width: 1.5; }
  .node.root circle { stroke: var(--fg); stroke-width: 3; }
  .label {
    fill: var(--fg);
    font-size: 10px;
    pointer-events: none;
    paint-order: stroke;
    stroke: var(--bg);
    stroke-width: 3px;
    stroke-linejoin: round;
  }
  .dim { opacity: 0.08; }
  #tip {
    position: absolute;
    z-index: 5;
    max-width: 340px;
    padding: 7px 9px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--panel);
    color: var(--fg);
    box-shadow: 0 3px 12px rgba(0,0,0,.18);
    pointer-events: none;
  }
  #tip .n { font-weight: 600; word-break: break-all; }
  #tip .m { color: var(--muted); margin-top: 2px; }
  #empty {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--muted);
  }
  [hidden] { display: none !important; }
  @media (max-width: 640px) {
    header { padding: 8px 12px; }
    input[type="search"] { width: 100%; }
  }
</style>
</head>
<body>
<header>
  <h1 id="title">kg graph</h1>
  <div id="counts"></div>
  <div class="spacer"></div>
  <input id="q" type="search" placeholder="Filter by name..." autocomplete="off" spellcheck="false">
  <button id="fit" type="button">Fit</button>
  <div id="legend"></div>
  <div id="warn" hidden></div>
</header>
<div id="stage">
  <svg id="canvas" aria-label="knowledge graph">
    <g id="viewport">
      <g id="edgeLayer"></g>
      <g id="nodeLayer"></g>
    </g>
  </svg>
  <div id="tip" hidden></div>
  <div id="empty" hidden>This graph slice has no nodes.</div>
</div>
<script type="application/json" id="kg-data">/*KG_DATA*/</script>
<script>
(function () {
  "use strict";

  var SVGNS = "http://www.w3.org/2000/svg";

  // Every tunable the page has, in one place. Nothing below this block should
  // carry a bare numeric literal: a constant buried in the middle of a force
  // calculation is unreadable and un-reviewable, and these in particular were
  // arrived at empirically, so they need somewhere to be seen and adjusted
  // together.
  var CFG = {
    // --- layout field ---
    fieldWidth: 1200,          // nominal layout area the graph settles into
    fieldHeight: 760,
    seedSpacing: 26,           // spiral spacing of the deterministic seed layout
    goldenAngle: 2.399963229728653,

    // --- forces ---
    repelRadius: 120,          // nodes closer than this push apart
    repelStrength: 0.55,
    linkDistance: 92,          // rest length of a relation
    linkStrength: 0.045,
    gravity: 0.0032,           // pull toward the centre, keeps the graph compact
    damping: 0.80,             // velocity retained per tick

    // --- integrator stability ---
    // A spring force proportional to (distance - linkDistance) grows without
    // bound, so a node far from its neighbour is pulled harder, overshoots
    // further, and the simulation diverges geometrically. These two caps make
    // that impossible and cost nothing at equilibrium.
    maxSpringExtension: 60,
    maxStepPerTick: 24,
    minSeparationSq: 0.01,     // below this, two nodes count as coincident

    // --- annealing ---
    alphaDecay: 0.99,
    alphaRest: 0.012,          // simulation is considered settled below this
    alphaWarmTarget: 0.05,     // pre-paint settle runs until alpha drops here
    reheatAlpha: 0.45,
    dragReheatAlpha: 0.3,
    fastTickAlpha: 0.35,       // while hotter than this, run several ticks a frame
    fastTicksPerFrame: 3,
    maxWarmIterations: 600,
    maxWarmIterationsLarge: 150,
    largeGraphNodes: 1200,     // above this, settle less before painting

    // --- node and label appearance ---
    nodeRadiusBase: 4,
    nodeRadiusMax: 7,          // most a high-degree node may add to the base
    nodeRadiusScale: 2.2,
    labelAllBelow: 140,        // label every node in graphs smaller than this
    labelHubDegree: 8,         // above this degree, label a node regardless
    labelMaxChars: 30,
    labelOffset: 3,

    // --- view ---
    minZoom: 0.08,
    maxZoom: 6,
    zoomSensitivity: 1.0015,
    fitPadding: 60,
    fitMaxScale: 2.5,          // never magnify a tiny graph past this
    tipOffset: 12,             // cursor-to-tooltip gap
    tipMargin: 8               // keep the tooltip this far inside the stage
  };
  var data = JSON.parse(document.getElementById("kg-data").textContent);
  var nodes = (data.nodes || []).slice();
  var rootID = data.root_id || "";

  var byID = Object.create(null);
  for (var i = 0; i < nodes.length; i++) {
    nodes[i].idx = i;
    byID[nodes[i].id] = nodes[i];
  }

  // An edge whose endpoints were filtered out of the node set would otherwise
  // read as a line to nowhere.
  var edges = [];
  var rawEdges = data.edges || [];
  for (var e = 0; e < rawEdges.length; e++) {
    var from = byID[rawEdges[e].from_id], to = byID[rawEdges[e].to_id];
    if (from && to) {
      edges.push({ a: from, b: to, type: rawEdges[e].type, derived: !!rawEdges[e].derived });
    }
  }

  var TYPE_COLORS = {
    file: "#4f8ff7",
    "function": "#3fb950",
    type: "#a371f7",
    "package": "#e3b341",
    "import": "#8b949e",
    topic: "#f78166"
  };
  function colorFor(t) {
    return TYPE_COLORS[String(t || "").toLowerCase()] || "#6e7681";
  }

  // ---- header ------------------------------------------------------------
  // Mirrors the comment header the mermaid and DOT renderers write, including
  // the truncation notice: a picture that quietly dropped half the graph is
  // worse than no picture.
  var derivedCount = 0;
  for (var d = 0; d < edges.length; d++) { if (edges[d].derived) { derivedCount++; } }

  var rootNode = rootID ? byID[rootID] : null;
  if (rootNode) {
    document.title = "kg graph: " + rootNode.name;
    document.getElementById("title").textContent = rootNode.name;
  }
  var counts = nodes.length + " node" + (nodes.length === 1 ? "" : "s") +
    ", " + edges.length + " relation" + (edges.length === 1 ? "" : "s");
  if (typeof data.total_nodes === "number" && data.total_nodes > 0) {
    counts += " of " + data.total_nodes + " in the project";
  }
  if (derivedCount > 0) {
    counts += " · " + derivedCount + " derived (dashed)";
  }
  document.getElementById("counts").textContent = counts;

  if (data.truncated) {
    var warn = document.getElementById("warn");
    warn.textContent = "Truncated at the node limit, so this is an incomplete picture — re-run with a higher --limit to see the rest.";
    warn.hidden = false;
  }
  if (!nodes.length) {
    document.getElementById("empty").hidden = false;
  }

  // ---- adjacency ---------------------------------------------------------
  var deg = Object.create(null);
  var nbrs = Object.create(null);
  function note(a, b) {
    deg[a] = (deg[a] || 0) + 1;
    (nbrs[a] || (nbrs[a] = Object.create(null)))[b] = true;
  }
  for (var k = 0; k < edges.length; k++) {
    note(edges[k].a.id, edges[k].b.id);
    note(edges[k].b.id, edges[k].a.id);
  }
  function radiusOf(n) {
    // Degree is the only signal here about which nodes anchor the picture.
    return CFG.nodeRadiusBase +
      Math.min(CFG.nodeRadiusMax, Math.sqrt(deg[n.id] || 0) * CFG.nodeRadiusScale);
  }

  // ---- deterministic initial layout --------------------------------------
  // A golden-angle spiral keyed on node index: spreads nodes evenly, needs no
  // randomness, and so keeps the rendered file reproducible.
  var W = CFG.fieldWidth, H = CFG.fieldHeight;
  for (var s = 0; s < nodes.length; s++) {
    var ang = s * CFG.goldenAngle;
    var rad = CFG.seedSpacing * Math.sqrt(s + 1);
    nodes[s].x = W / 2 + rad * Math.cos(ang);
    nodes[s].y = H / 2 + rad * Math.sin(ang);
    nodes[s].vx = 0;
    nodes[s].vy = 0;
  }

  // ---- force simulation --------------------------------------------------
  // Repulsion is bucketed into a uniform grid of cell size REPEL, so each node
  // only considers the nine cells around it. That keeps a tick linear in node
  // count instead of quadratic, which is what makes --limit 0 on a few
  // thousand nodes merely slow rather than hopeless.
  // MAXSTEP and MAXPULL are what keep explicit integration stable. A spring
  // force proportional to (distance - LINK) grows without bound, so a node
  // that lands far from its neighbour gets pulled harder, overshoots further,
  // and the simulation diverges geometrically — positions reach 1e33 within a
  // few hundred ticks. Clamping the spring extension and capping per-tick
  // speed costs nothing at equilibrium and makes divergence impossible.
  var REPEL = CFG.repelRadius, LINK = CFG.linkDistance, alpha = 1;

  function tick() {
    var grid = Object.create(null), key, n, m, j, dx, dy, d2, dist, f;
    for (j = 0; j < nodes.length; j++) {
      n = nodes[j];
      key = Math.floor(n.x / REPEL) + ":" + Math.floor(n.y / REPEL);
      (grid[key] || (grid[key] = [])).push(n);
    }
    for (j = 0; j < nodes.length; j++) {
      n = nodes[j];
      var cx = Math.floor(n.x / REPEL), cy = Math.floor(n.y / REPEL);
      for (var ox = -1; ox <= 1; ox++) {
        for (var oy = -1; oy <= 1; oy++) {
          var bucket = grid[(cx + ox) + ":" + (cy + oy)];
          if (!bucket) { continue; }
          for (var bi = 0; bi < bucket.length; bi++) {
            m = bucket[bi];
            if (m === n) { continue; }
            dx = n.x - m.x; dy = n.y - m.y;
            d2 = dx * dx + dy * dy;
            if (d2 > REPEL * REPEL) { continue; }
            if (d2 < CFG.minSeparationSq) {
              // Exactly coincident nodes have no direction to separate along;
              // index order gives a stable one.
              dx = (n.idx - m.idx) || 1; dy = 1; d2 = dx * dx + dy * dy;
            }
            dist = Math.sqrt(d2);
            f = (REPEL - dist) / dist * CFG.repelStrength;
            n.vx += dx * f; n.vy += dy * f;
          }
        }
      }
    }
    for (j = 0; j < edges.length; j++) {
      var a = edges[j].a, b = edges[j].b;
      dx = b.x - a.x; dy = b.y - a.y;
      dist = Math.sqrt(dx * dx + dy * dy) || 1;
      var pull = Math.max(-CFG.maxSpringExtension,
        Math.min(CFG.maxSpringExtension, dist - LINK));
      f = pull / dist * CFG.linkStrength;
      a.vx += dx * f; a.vy += dy * f;
      b.vx -= dx * f; b.vy -= dy * f;
    }
    for (j = 0; j < nodes.length; j++) {
      n = nodes[j];
      n.vx += (W / 2 - n.x) * CFG.gravity;
      n.vy += (H / 2 - n.y) * CFG.gravity;
      if (n.pinned) { n.vx = 0; n.vy = 0; continue; }
      var speed = Math.sqrt(n.vx * n.vx + n.vy * n.vy);
      if (speed > CFG.maxStepPerTick) {
        n.vx = n.vx / speed * CFG.maxStepPerTick;
        n.vy = n.vy / speed * CFG.maxStepPerTick;
      }
      n.x += n.vx * alpha; n.y += n.vy * alpha;
      n.vx *= CFG.damping; n.vy *= CFG.damping;
    }
    alpha *= CFG.alphaDecay;
  }

  // ---- DOM ---------------------------------------------------------------
  var svg = document.getElementById("canvas");
  var viewport = document.getElementById("viewport");
  var edgeLayer = document.getElementById("edgeLayer");
  var nodeLayer = document.getElementById("nodeLayer");
  var tip = document.getElementById("tip");
  var stage = document.getElementById("stage");

  // Labelling every node in a dense graph is noise; labelling none of them
  // makes the picture unreadable. Label what carries the shape: the root, and
  // the hubs everything else hangs off.
  var labelAll = nodes.length <= CFG.labelAllBelow;
  function labelled(n) {
    return labelAll || n.id === rootID || (deg[n.id] || 0) >= CFG.labelHubDegree;
  }

  // String.length counts UTF-16 code units, so a naive slice can land between
  // the halves of a surrogate pair and leave a lone half the font draws as a
  // replacement glyph. Entity names are usually source identifiers, but topic
  // names are freeform and may hold anything, so step back off a split pair.
  function ellipsize(s, max) {
    if (s.length <= max) { return s; }
    var cut = max - 1;
    var last = s.charCodeAt(cut - 1);
    if (last >= 0xD800 && last <= 0xDBFF) { cut--; }
    return s.slice(0, cut) + "…";
  }

  for (var ei = 0; ei < edges.length; ei++) {
    var line = document.createElementNS(SVGNS, "line");
    line.setAttribute("class", edges[ei].derived ? "edge derived" : "edge");
    edges[ei].el = line;
    edgeLayer.appendChild(line);
  }

  for (var ni = 0; ni < nodes.length; ni++) {
    var node = nodes[ni];
    var g = document.createElementNS(SVGNS, "g");
    g.setAttribute("class", node.id === rootID ? "node root" : "node");
    var circle = document.createElementNS(SVGNS, "circle");
    circle.setAttribute("r", radiusOf(node));
    circle.setAttribute("fill", colorFor(node.type));
    g.appendChild(circle);
    if (labelled(node)) {
      var text = document.createElementNS(SVGNS, "text");
      text.setAttribute("class", "label");
      text.setAttribute("x", radiusOf(node) + CFG.labelOffset);
      text.setAttribute("y", CFG.labelOffset);
      // textContent, never innerHTML: an entity name is data.
      text.textContent = ellipsize(node.name, CFG.labelMaxChars);
      g.appendChild(text);
    }
    node.el = g;
    bind(node, g);
    nodeLayer.appendChild(g);
  }

  function draw() {
    for (var j = 0; j < edges.length; j++) {
      var ed = edges[j];
      ed.el.setAttribute("x1", ed.a.x);
      ed.el.setAttribute("y1", ed.a.y);
      ed.el.setAttribute("x2", ed.b.x);
      ed.el.setAttribute("y2", ed.b.y);
    }
    for (var i2 = 0; i2 < nodes.length; i2++) {
      nodes[i2].el.setAttribute("transform", "translate(" + nodes[i2].x + "," + nodes[i2].y + ")");
    }
  }

  var running = true;
  function frame() {
    if (running) {
      var steps = alpha > CFG.fastTickAlpha ? CFG.fastTicksPerFrame : 1;
      for (var st = 0; st < steps; st++) { tick(); }
      draw();
      if (alpha < CFG.alphaRest) {
        running = false;
        if (!userMoved) { fit(); }
      }
    }
    requestAnimationFrame(frame);
  }
  function reheat(a) {
    alpha = Math.max(alpha, a || CFG.reheatAlpha);
    running = true;
  }
  // Started below, once the view transform it draws through exists.

  // ---- view transform ----------------------------------------------------
  var view = { k: 1, x: 0, y: 0 };
  // Once the reader has framed the graph themselves, nothing may re-frame it
  // out from under them.
  var userMoved = false;
  function applyView() {
    viewport.setAttribute("transform",
      "translate(" + view.x + "," + view.y + ") scale(" + view.k + ")");
  }
  function fit() {
    if (!nodes.length) { return; }
    var minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (var j = 0; j < nodes.length; j++) {
      if (nodes[j].x < minX) { minX = nodes[j].x; }
      if (nodes[j].y < minY) { minY = nodes[j].y; }
      if (nodes[j].x > maxX) { maxX = nodes[j].x; }
      if (nodes[j].y > maxY) { maxY = nodes[j].y; }
    }
    var pad = CFG.fitPadding;
    var box = svg.getBoundingClientRect();
    var sw = Math.max(1, maxX - minX + pad * 2), sh = Math.max(1, maxY - minY + pad * 2);
    view.k = Math.min(box.width / sw, box.height / sh, CFG.fitMaxScale);
    view.x = box.width / 2 - ((minX + maxX) / 2) * view.k;
    view.y = box.height / 2 - ((minY + maxY) / 2) * view.k;
    applyView();
  }
  document.getElementById("fit").addEventListener("click", function () {
    userMoved = false;
    fit();
  });
  // Guarded by userMoved for the same reason the settle path above is: a resize
  // is not a request to be re-framed. Hit "fit" to get the automatic framing back.
  window.addEventListener("resize", function () { if (!running && !userMoved) { fit(); } });

  svg.addEventListener("wheel", function (ev) {
    ev.preventDefault();
    var box = svg.getBoundingClientRect();
    var mx = ev.clientX - box.left, my = ev.clientY - box.top;
    userMoved = true;
    var factor = Math.pow(CFG.zoomSensitivity, -ev.deltaY);
    var next = Math.min(CFG.maxZoom, Math.max(CFG.minZoom, view.k * factor));
    // Keep the point under the cursor fixed while zooming.
    view.x = mx - (mx - view.x) * (next / view.k);
    view.y = my - (my - view.y) * (next / view.k);
    view.k = next;
    applyView();
  }, { passive: false });

  var panning = null;
  svg.addEventListener("pointerdown", function (ev) {
    if (ev.target.closest && ev.target.closest(".node")) { return; }
    userMoved = true;
    panning = { x: ev.clientX, y: ev.clientY, vx: view.x, vy: view.y };
    svg.classList.add("panning");
    svg.setPointerCapture(ev.pointerId);
  });
  svg.addEventListener("pointermove", function (ev) {
    if (!panning) { return; }
    view.x = panning.vx + (ev.clientX - panning.x);
    view.y = panning.vy + (ev.clientY - panning.y);
    applyView();
  });
  function endPan() { panning = null; svg.classList.remove("panning"); }
  svg.addEventListener("pointerup", endPan);
  svg.addEventListener("pointercancel", endPan);

  // ---- node interaction --------------------------------------------------
  function bind(node, g) {
    var drag = null;
    g.addEventListener("pointerdown", function (ev) {
      ev.stopPropagation();
      var p = toGraph(ev);
      drag = { dx: node.x - p.x, dy: node.y - p.y, moved: false };
      node.pinned = true;
      g.setPointerCapture(ev.pointerId);
    });
    g.addEventListener("pointermove", function (ev) {
      if (!drag) { return; }
      var p = toGraph(ev);
      node.x = p.x + drag.dx;
      node.y = p.y + drag.dy;
      drag.moved = true;
      reheat(CFG.dragReheatAlpha);
    });
    g.addEventListener("pointerup", function (ev) {
      if (drag && !drag.moved) { select(selected === node.id ? null : node.id); }
      if (drag) { node.pinned = false; }
      drag = null;
      ev.stopPropagation();
    });
    // A drag that ends in a cancel rather than a pointerup gets no pointerup
    // at all — a system gesture interrupting a touch is the usual way. Without
    // this the node keeps pinned set, and tick() zeroes its velocity forever,
    // so it sits frozen through every later re-layout. No select() here: a
    // cancelled gesture is not a click.
    g.addEventListener("pointercancel", function () {
      if (drag) { node.pinned = false; }
      drag = null;
    });
    g.addEventListener("pointerenter", function (ev) { showTip(node, ev); });
    g.addEventListener("pointerleave", function () { tip.hidden = true; });
  }

  function toGraph(ev) {
    var box = svg.getBoundingClientRect();
    return {
      x: (ev.clientX - box.left - view.x) / view.k,
      y: (ev.clientY - box.top - view.y) / view.k
    };
  }

  function showTip(node, ev) {
    tip.textContent = "";
    var name = document.createElement("div");
    name.className = "n";
    name.textContent = node.name;
    tip.appendChild(name);
    var meta = document.createElement("div");
    meta.className = "m";
    var bits = [node.type];
    if (rootID) { bits.push("depth " + (node.depth || 0)); }
    bits.push((deg[node.id] || 0) + " relation" + ((deg[node.id] || 0) === 1 ? "" : "s"));
    if (node.layers && node.layers.length) { bits.push(node.layers.join(", ")); }
    meta.textContent = bits.join(" · ");
    tip.appendChild(meta);
    tip.hidden = false;
    var box = stage.getBoundingClientRect();
    var x = ev.clientX - box.left + CFG.tipOffset;
    var y = ev.clientY - box.top + CFG.tipOffset;
    if (x + tip.offsetWidth > box.width - CFG.tipMargin) {
      x = box.width - tip.offsetWidth - CFG.tipMargin;
    }
    if (y + tip.offsetHeight > box.height - CFG.tipMargin) {
      y = ev.clientY - box.top - tip.offsetHeight - CFG.tipOffset;
    }
    tip.style.left = Math.max(CFG.tipMargin / 2, x) + "px";
    tip.style.top = Math.max(CFG.tipMargin / 2, y) + "px";
  }

  // ---- filtering ---------------------------------------------------------
  var selected = null;
  var hiddenTypes = Object.create(null);
  var query = "";

  function select(id) {
    selected = id;
    refresh();
  }

  function visible(node) {
    if (hiddenTypes[String(node.type || "").toLowerCase()]) { return false; }
    if (query && node.name.toLowerCase().indexOf(query) === -1) { return false; }
    return true;
  }

  // Selection dims everything outside the chosen node's immediate
  // neighbourhood rather than removing it, so the surrounding shape stays
  // legible as context.
  function inFocus(node) {
    if (!selected) { return true; }
    if (node.id === selected) { return true; }
    return !!(nbrs[selected] && nbrs[selected][node.id]);
  }

  function refresh() {
    for (var j = 0; j < nodes.length; j++) {
      var node = nodes[j];
      var ok = visible(node) && inFocus(node);
      node.shown = ok;
      node.el.classList.toggle("dim", !ok);
    }
    for (var k2 = 0; k2 < edges.length; k2++) {
      var ed = edges[k2];
      ed.el.classList.toggle("dim", !(ed.a.shown && ed.b.shown));
    }
  }

  var qInput = document.getElementById("q");
  qInput.addEventListener("input", function () {
    query = qInput.value.trim().toLowerCase();
    refresh();
  });
  qInput.addEventListener("keydown", function (ev) {
    if (ev.key === "Escape") { qInput.value = ""; query = ""; refresh(); }
  });

  // Legend doubles as the type filter; counts tell you what a toggle costs.
  var typeCounts = Object.create(null);
  var typeOrder = [];
  for (var t = 0; t < nodes.length; t++) {
    var ty = String(nodes[t].type || "").toLowerCase();
    if (!(ty in typeCounts)) { typeCounts[ty] = 0; typeOrder.push(ty); }
    typeCounts[ty]++;
  }
  typeOrder.sort(function (a, b) { return typeCounts[b] - typeCounts[a] || (a < b ? -1 : 1); });
  var legend = document.getElementById("legend");
  for (var li = 0; li < typeOrder.length; li++) {
    (function (ty) {
      var label = document.createElement("label");
      var box = document.createElement("input");
      box.type = "checkbox";
      box.checked = true;
      box.addEventListener("change", function () {
        hiddenTypes[ty] = !box.checked;
        refresh();
      });
      var swatch = document.createElement("span");
      swatch.className = "swatch";
      swatch.style.background = colorFor(ty);
      var name = document.createElement("span");
      name.textContent = ty || "untyped";
      var count = document.createElement("span");
      count.className = "count";
      count.textContent = typeCounts[ty];
      label.appendChild(box);
      label.appendChild(swatch);
      label.appendChild(name);
      label.appendChild(count);
      legend.appendChild(label);
    })(typeOrder[li]);
  }

  document.addEventListener("keydown", function (ev) {
    if (ev.key === "Escape" && selected) { select(null); }
  });

  // Settle synchronously before the first paint. Animating the seed spiral as
  // it unwinds looks busy and means the opening view frames a layout that is
  // still moving; running the simulation to rest first means the page opens on
  // the picture the reader wants. The iteration guard keeps a very large
  // --limit from freezing the tab: it stops early and the graph simply opens
  // less relaxed.
  var maxWarm = nodes.length > CFG.largeGraphNodes
    ? CFG.maxWarmIterationsLarge
    : CFG.maxWarmIterations;
  for (var warm = 0; warm < maxWarm && alpha > CFG.alphaWarmTarget; warm++) { tick(); }
  draw();
  fit();
  running = false;
  requestAnimationFrame(frame);
  refresh();
})();
</script>
</body>
</html>
`
