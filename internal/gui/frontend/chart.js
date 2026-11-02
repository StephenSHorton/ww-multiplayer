// chart.js: the Session screen's sea chart (wind#51). The Great Sea's 7x7
// squares at true scale, the island art read from the player's own disc
// (Bridge.SeaChart; just the grid and the names until it is there), and
// every Link where it is: on the sea at its position, in a house, cave or
// dungeon pinned to its island. Drag to pan, wheel or pinch to zoom; Fit
// all and Follow me. Go places the dots (game.go mapView); this file only
// draws them.
//
// Chart units are world units / 1000: the sea spans -350..350 on both
// axes, +x east, +y south (world Z), as the game's chart is drawn.

import { call } from "./transport.js";
import { $, el } from "./ui.js";

const SVGNS = "http://www.w3.org/2000/svg";
const K = 1000; // world units per chart unit
const SEA = 350; // the sea's half-width, chart units
const SQ = 100; // one square
const MIN_W = 20; // the closest zoom: one island tile (20000 units, 800 px of art) fills the chart
const MAX_W = 900; // the farthest: the whole sea and its margin
const PAD = 24; // px around the dots that Fit all keeps

function svg(tag, attrs = {}, text) {
	const n = document.createElementNS(SVGNS, tag);
	for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
	if (text !== undefined) n.textContent = text;
	return n;
}

const chart = {
	built: false,
	view: { x: -SEA - 20, y: -SEA - 20, w: 2 * SEA + 40 }, // the viewBox (square)
	fitted: false, // the first data has been fitted
	follow: false,
	rev: -1, // the island art drawn
	fetching: false,
	names: null,
	m: null, // the last MapView
	pointers: new Map(),
	pinch: null,
};

// drawMap draws m, the state's MapView.
export function drawMap(m) {
	if (!chart.built) build();
	chart.m = m;
	if (m.chartRev !== chart.rev && !chart.fetching) loadArt(m.chartRev);
	if (!chart.fitted && (m.selfOk || m.players.some((p) => p.drawn))) {
		chart.fitted = true;
		fitAll();
	}
	if (chart.follow && m.selfOk) centerOn(m.self.x / K, m.self.z / K);
	drawDots();
	drawNote(m);
}

function build() {
	chart.built = true;
	const s = $("map");
	s.removeAttribute("viewBox");
	s.replaceChildren(
		svg("rect", { class: "sea", x: -SEA, y: -SEA, width: 2 * SEA, height: 2 * SEA }),
		svg("g", { id: "chart-islands" }),
		svg("g", { id: "chart-grid" }),
		svg("g", { id: "chart-names" }),
		svg("g", { id: "chart-dots" }),
	);
	const grid = $("chart-grid");
	for (let i = 0; i <= 7; i++) {
		const v = -SEA + i * SQ;
		grid.append(svg("line", { x1: v, y1: -SEA, x2: v, y2: SEA }), svg("line", { x1: -SEA, y1: v, x2: SEA, y2: v }));
	}
	s.addEventListener("pointerdown", onDown);
	s.addEventListener("pointermove", onMove);
	s.addEventListener("pointerup", onUp);
	s.addEventListener("pointercancel", onUp);
	s.addEventListener("wheel", onWheel, { passive: false });
	s.addEventListener("dblclick", (e) => zoomAt(e, 0.5));
	$("chart-fit").addEventListener("click", () => { chart.follow = false; syncFollow(); fitAll(); });
	$("chart-follow").addEventListener("click", () => {
		chart.follow = !chart.follow;
		syncFollow();
		if (chart.follow && chart.m && chart.m.selfOk) {
			if (chart.view.w > 120) setView(chart.view.x, chart.view.y, 120);
			centerOn(chart.m.self.x / K, chart.m.self.z / K);
		}
		drawDots();
	});
	$("chart-in").addEventListener("click", () => zoomCenter(0.6));
	$("chart-out").addEventListener("click", () => zoomCenter(1 / 0.6));
	new ResizeObserver(() => applyView()).observe(s);
	syncFollow();
	applyView();
}

function syncFollow() {
	$("chart-follow").setAttribute("aria-pressed", String(chart.follow));
}

// ---------- the island art ----------

async function loadArt(rev) {
	chart.fetching = true;
	try {
		const c = await call("SeaChart");
		chart.names = c.names;
		const g = $("chart-islands");
		g.replaceChildren(...c.islands.map((is) => svg("image", {
			href: is.src, x: (is.cx - c.tileUnits / 2) / K, y: (is.cz - c.tileUnits / 2) / K,
			width: c.tileUnits / K, height: c.tileUnits / K, preserveAspectRatio: "none",
		})));
		chart.rev = c.rev;
		$("map").classList.toggle("noart", !c.islands.length);
		drawNames();
	} catch {
		chart.rev = rev; // don't ask again until the art changes
	} finally {
		chart.fetching = false;
	}
}

// drawNames writes the squares' names (A1..G7, and the islands' when
// zoomed in), at a constant size on screen.
function drawNames() {
	const g = $("chart-names");
	const u = unitPx();
	const close = chart.view.w <= 260;
	const kids = [];
	for (let r = 0; r < 7; r++) {
		for (let c = 0; c < 7; c++) {
			const x = -SEA + c * SQ, y = -SEA + r * SQ;
			kids.push(svg("text", { class: "sqname", x: x + 5 * u, y: y + 15 * u, "font-size": 12 * u, "stroke-width": 2 * u }, String.fromCharCode(65 + c) + (r + 1)));
			if (close && chart.names) {
				kids.push(svg("text", { class: "isname", x: x + SQ / 2, y: y + SQ - 8 * u, "font-size": 12 * u, "stroke-width": 2.5 * u, "text-anchor": "middle" }, chart.names[r][c]));
			}
		}
	}
	g.replaceChildren(...kids);
}

// ---------- the dots ----------

function drawDots() {
	const m = chart.m;
	if (!m) return;
	const u = unitPx();
	const kids = [];
	const all = m.players.filter((p) => p.drawn).map((p) => ({ ...p, self: false }));
	if (m.selfOk) all.push({ ...m.self, color: m.color, self: true });
	// Links pinned to one island are spread around its centre.
	const groups = new Map();
	for (const p of all) {
		if (!p.pinned) continue;
		const k = p.x + "," + p.z;
		if (!groups.has(k)) groups.set(k, []);
		groups.get(k).push(p);
	}
	for (const g of groups.values()) {
		if (g.length < 2) continue;
		g.forEach((p, i) => {
			const a = (i / g.length) * 2 * Math.PI - Math.PI / 2;
			p.ox = Math.cos(a) * 14 * u;
			p.oy = Math.sin(a) * 14 * u;
		});
	}
	const mine = [];
	const labels = []; // placed name boxes, chart units, so names don't overlap
	const place = (x, y, chars) => {
		const w = chars * 7.5 * u, h = 14 * u;
		for (const dy of [0, 14, -14, 28, -28, 42, -42]) {
			const b = { x0: x + 11 * u, y0: y - 9 * u + dy * u, x1: x + 11 * u + w, y1: y - 9 * u + dy * u + h };
			if (!labels.some((o) => b.x0 < o.x1 && o.x0 < b.x1 && b.y0 < o.y1 && o.y0 < b.y1)) {
				labels.push(b);
				return dy * u;
			}
		}
		return 0;
	};
	for (const p of all.sort((a, b) => b.self - a.self)) {
		const x = p.x / K + (p.ox || 0), y = p.z / K + (p.oy || 0);
		const tip = (p.self ? "You" : p.name) + " · " + p.square + " " + (p.where || p.island) + (p.pinned ? "" : " (at sea)");
		const dot = svg(p.pinned ? "rect" : "circle", p.pinned
			? { class: "dot pin" + (p.self ? " self" : ""), x: x - 7 * u, y: y - 7 * u, width: 14 * u, height: 14 * u, transform: `rotate(45 ${x} ${y})`, fill: p.color || "#3c9e2e" }
			: { class: "dot" + (p.self ? " self" : ""), cx: x, cy: y, r: (p.self ? 8 : 7) * u, fill: p.color || "#3c9e2e" });
		dot.append(svg("title", {}, tip));
		const name = p.self ? "You" : p.name;
		const dy = place(x, y, name.length);
		const pair = [dot, svg("text", { class: "who", x: x + 11 * u, y: y + 4 * u + dy, "font-size": 12 * u, "stroke-width": 3 * u }, name)];
		if (p.self) mine.push(...pair); // drawn last, on top
		else kids.push(...pair);
	}
	$("chart-dots").replaceChildren(...kids, ...mine);
}

function drawNote(m) {
	const line = (name, p) => el("li", {},
		el("b", {}, name), " ",
		!p.drawn ? "in " + p.where + " (not on the chart)" : p.square + " · " + (p.pinned ? p.where : p.island + ", at sea"));
	const items = [];
	if (m.selfOk) items.push(line("You", m.self));
	for (const p of m.players) items.push(line(p.name, p));
	const note = $("map-note");
	if (!items.length) {
		note.replaceChildren("Everyone shows here once their Link is in the world.");
		return;
	}
	note.replaceChildren(el("ul", { class: "wherelist" }, items),
		$("map").classList.contains("noart") ? "The islands are drawn from your disc once it has been read (patch it in Setup)." : "");
}

// ---------- the view ----------

// unitPx is how many chart units one screen pixel is.
function unitPx() {
	const w = $("map").clientWidth || 300;
	return chart.view.w / w;
}

function setView(x, y, w) {
	w = Math.min(MAX_W, Math.max(MIN_W, w));
	// keep some of the sea in view
	const lim = SEA + 40;
	x = Math.min(lim - w * 0.2, Math.max(-lim - w * 0.8, x));
	y = Math.min(lim - w * 0.2, Math.max(-lim - w * 0.8, y));
	chart.view = { x, y, w };
	applyView();
}

let frame = 0;
function applyView() {
	if (frame) return;
	frame = requestAnimationFrame(() => {
		frame = 0;
		const v = chart.view;
		$("map").setAttribute("viewBox", `${v.x} ${v.y} ${v.w} ${v.w}`);
		drawNames();
		drawDots();
	});
}

function centerOn(cx, cy) {
	setView(cx - chart.view.w / 2, cy - chart.view.w / 2, chart.view.w);
}

function fitAll() {
	const m = chart.m;
	const pts = [];
	if (m) {
		if (m.selfOk) pts.push(m.self);
		for (const p of m.players) if (p.drawn) pts.push(p);
	}
	if (!pts.length) {
		setView(-SEA - 20, -SEA - 20, 2 * SEA + 40);
		return;
	}
	const xs = pts.map((p) => p.x / K), ys = pts.map((p) => p.z / K);
	const x0 = Math.min(...xs), x1 = Math.max(...xs), y0 = Math.min(...ys), y1 = Math.max(...ys);
	const px = $("map").clientWidth || 300;
	let w = Math.max(x1 - x0, y1 - y0, 40);
	w = w * px / Math.max(px - 2 * PAD - 80, 60); // room for the names to the right
	if (w > 2 * SEA * 0.8) {
		setView(-SEA - 20, -SEA - 20, 2 * SEA + 40); // most of the sea: show all of it
		return;
	}
	setView((x0 + x1) / 2 - w / 2, (y0 + y1) / 2 - w / 2, w);
}

// toChart is the chart point under client (cx, cy).
function toChart(cx, cy) {
	const r = $("map").getBoundingClientRect();
	const v = chart.view;
	return { x: v.x + (cx - r.left) / r.width * v.w, y: v.y + (cy - r.top) / r.height * v.w };
}

function zoomAround(p, f) {
	const v = chart.view;
	const w = Math.min(MAX_W, Math.max(MIN_W, v.w * f));
	const k = w / v.w;
	setView(p.x - (p.x - v.x) * k, p.y - (p.y - v.y) * k, w);
}

function zoomAt(e, f) {
	e.preventDefault();
	zoomAround(chart.follow && chart.m && chart.m.selfOk ? { x: chart.m.self.x / K, y: chart.m.self.z / K } : toChart(e.clientX, e.clientY), f);
}

function zoomCenter(f) {
	const v = chart.view;
	zoomAround({ x: v.x + v.w / 2, y: v.y + v.w / 2 }, f);
}

function onWheel(e) {
	const f = Math.exp(e.deltaY * (e.deltaMode === 1 ? 0.05 : 0.0015));
	zoomAt(e, f);
}

function onDown(e) {
	$("map").setPointerCapture(e.pointerId);
	chart.pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
	if (chart.pointers.size === 2) startPinch();
}

function onMove(e) {
	const prev = chart.pointers.get(e.pointerId);
	if (!prev) return;
	const now = { x: e.clientX, y: e.clientY };
	chart.pointers.set(e.pointerId, now);
	if (chart.pointers.size >= 2 && chart.pinch) {
		const [a, b] = [...chart.pointers.values()];
		const d = Math.hypot(a.x - b.x, a.y - b.y);
		const mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
		const p = chart.pinch;
		const w = Math.min(MAX_W, Math.max(MIN_W, p.w * p.d / Math.max(d, 1)));
		const r = $("map").getBoundingClientRect();
		// the chart point that was under the pinch's midpoint stays under it
		setView(p.at.x - (mid.x - r.left) / r.width * w, p.at.y - (mid.y - r.top) / r.height * w, w);
		return;
	}
	const r = $("map").getBoundingClientRect();
	const dx = (now.x - prev.x) / r.width * chart.view.w, dy = (now.y - prev.y) / r.height * chart.view.w;
	if (dx || dy) {
		if (chart.follow) { chart.follow = false; syncFollow(); }
		setView(chart.view.x - dx, chart.view.y - dy, chart.view.w);
	}
}

function startPinch() {
	const [a, b] = [...chart.pointers.values()];
	chart.pinch = { d: Math.hypot(a.x - b.x, a.y - b.y), w: chart.view.w, at: toChart((a.x + b.x) / 2, (a.y + b.y) / 2) };
	if (chart.follow) { chart.follow = false; syncFollow(); }
}

function onUp(e) {
	chart.pointers.delete(e.pointerId);
	if (chart.pointers.size < 2) chart.pinch = null;
}
