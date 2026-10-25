// app.js: the window's screens: Home (name, color, Host / Join, the
// sessions on your network, recent hosts) and Session, which is the Lobby (App PR 7: members with their
// colors, ready and connection, your tunic and Ready, the host's panel
// with the share card, co-op and Start, chat, Kick), then, once the host
// pressed Start, the game (App PR 8: every player's Dolphin and area, the
// minimap, chat, the reconnect and "Dolphin closed" banners), or a direct
// session's players. Everything reaches Go through transport.js. The state comes
// whole from Bridge.State, re-read on every "state" event and once a
// second while a session runs, so there is no diffing here: each render
// rebuilds its lists. The dialogs a join error opens and the reconnect
// banner come in the state too (Go decides them: internal/gui/lobby.go).

import { call, onEvent, mode } from "./transport.js";
import { $, el, toast, swatches, shade, openDialog, closeDialog, dialogKey, setDialogText, initDialog } from "./ui.js";
import { initSetup, openSetup, onPatch, patchRunning } from "./setup.js";
import { renderCoop, dialogExtra, coopAction, initCoop } from "./coop.js";
import { initMods, refreshMods } from "./mods.js";
import { initUpdate, startUpdate, onUpdate, render as renderUpdate, renderSettings as renderUpdateSettings } from "./update.js";
import { initTheme } from "./theme.js";

const LOG_MAX = 500;

let info = null; // Bridge.Info
let state = null; // Bridge.State
let view = "home"; // "home", "session", "setup" (setup.js), "mods" (mods.js) or "settings"
let beforeMods = "home"; // the screen the Mods page's Back returns to
let session = 0; // the session the Session screen shows
let homeColor = ""; // the Home form's color, a "#RRGGBB"
let left = false; // the user pressed Leave (no "ended" panel then)
let busy = false;
let shared = { session: 0, port: 0, addrs: [] }; // the host's share card, read once per session
let shareSeq = 0; // the newest ShareAddrs call: an older reply is dropped
let lan = { on: false, sessions: [] }; // Bridge.LanSessions, polled while Home shows
let lanSeq = 0; // the newest LanSessions call
// IP addresses are masked on screen (owner decision 2026-10-16,
// streamer-safe): Go masks them in every view unless the header's "Show
// IP addresses" switch (info.showIPs) is on. An address field Go sends
// with its masked form (masked, addrMasked) is drawn masked unless the
// switch is on when it is drawn, and a state that came unmasked is
// dropped once the switch is off (a reply from before it was turned off
// can arrive after). A share-card row's Show
// reveals it until the screen is left. Holds the revealed addresses.
let revealed = new Set();
// The log and chat lines drawn unmasked (the switch was on), each with
// its masked text and sender, so turning the switch off masks them again.
const unmasked = new WeakMap();
const closedDialogs = new Set(); // keys of state dialogs the user closed
const liveDialogs = new Set(); // keys of live state dialogs: closed once the state drops them

const PHASES = { connecting: "Connecting…", lobby: "Lobby", starting: "Starting…", "in game": "In game", ended: "Ended", idle: "" };
// Notices that end the session: its dialog says it, not a toast.
const FATAL_NOTICES = new Set(["name_taken", "full", "kicked", "mods_differ", "replaced"]);

// A crown for the host's row (drawn here, no game assets).
const CROWN = '<svg viewBox="0 0 24 18" aria-hidden="true"><path d="M2 16 1 4l6 5 5-8 5 8 6-5-1 12Z" fill="#ffe63a" stroke="#142408" stroke-width="2" stroke-linejoin="round"/><circle cx="12" cy="11" r="1.8" fill="#e0302a"/></svg>';

// ---------- color swatches ----------

function renderHomeSwatches() {
	swatches($("home-swatches"), info.palette, homeColor, (hex) => { homeColor = hex; renderHomeSwatches(); });
	tintLink($("home-link").firstElementChild, homeColor);
	renderSide();
}

// tintLink dresses a copy of the Setup's Link figure in color.
function tintLink(fig, color) {
	if (!fig || !color) return;
	fig.style.setProperty("--tunic", color);
	fig.style.setProperty("--tunic-dk", shade(color, 0.62));
	fig.style.setProperty("--tunic-hi", shade(color, 1.25));
}

// ---------- the sidebar ----------

// renderSide draws the sidebar: the page you are on, Play (Home, or the
// running session with its live dot), Setup (not during a session: it
// patches the disc the game runs) and your card: your name and tunic,
// and where you are.
function renderSide() {
	const running = !!(state && state.running);
	const page = view === "session" || view === "home" ? "play" : view;
	for (const b of document.querySelectorAll(".nav [data-nav]")) {
		if (b.dataset.nav === page) b.setAttribute("aria-current", "page");
		else b.removeAttribute("aria-current");
	}
	$("nav-play-label").textContent = running ? "Session" : "Play";
	$("nav-live").hidden = !running;
	const setup = $("setup-btn");
	setup.disabled = running;
	setup.title = running ? "Setup waits until you leave the session" : "";
	const lobby = state && state.lobby;
	const self = lobby && lobby.members ? lobby.members.find((m) => m.self) : null;
	const name = running && state.name ? state.name : $("name").value.trim() || info.name || "You";
	$("me-name").textContent = name;
	$("me-dot").style.setProperty("--c", (self && self.color) || (running && state.color) || homeColor || "#3C9A2E");
	let status = "Not in a session";
	if (running) {
		// A pre-lobby host's lobby is legacy and lists no members (as render()).
		const n = lobby && !lobby.legacy ? lobby.members.length : state.players.length + 1;
		status = (PHASES[state.phase] || state.phase) + " · " + n + (n === 1 ? " player" : " players");
	}
	$("me-status").textContent = status;
}

// ---------- screens ----------

// show opens screen v. Leaving Setup while a disc is being patched asks
// first: the patch goes on, but Host and Join wait for it (Go refuses
// them, errPatching), so Setup is where its progress and Stop are.
function show(v, force = false) {
	if (!force && view === "setup" && v !== "setup" && patchRunning()) {
		openDialog({
			key: "leave-setup",
			title: "Your disc is still being patched",
			text: "It keeps going if you leave Setup, but hosting and joining wait until it's done. Setup shows its progress and can stop it.",
			actions: [{ label: "Stay in Setup", primary: true }, { label: "Leave Setup", run: () => show(v, true) }],
		});
		return;
	}
	if (v !== view) {
		revealed = new Set();
		setAddrShown(info.showIPs);
	}
	view = v;
	for (const id of ["home", "session", "setup", "mods", "settings"]) $(id).hidden = v !== id;
	if (v === "mods") refreshMods();
	if (v === "settings") renderUpdateSettings();
	renderUpdate(); // the update banner: Home, Mods and Settings only, never in a session
	renderSide();
	$("bar").hidden = v !== "session";
	if (v !== "session") $("leave-btn").hidden = true;
	if (v === "home") {
		$("home-error").textContent = "";
		renderRecent();
		refreshLan();
		// A join that reached its host saved it as a recent host meanwhile.
		call("Info").then((i) => { info = i; renderRecent(); }).catch(() => {});
	}
	render();
}

// fillHome puts the saved name and color into the Home form (at start, and
// after the setup saved them).
function fillHome() {
	if (!$("home-link").firstElementChild) {
		const fig = $("s-link").cloneNode(true);
		fig.removeAttribute("id");
		fig.setAttribute("role", "presentation");
		$("home-link").append(fig);
	}
	$("name").value = info.name || "";
	// Players may share a color, and one who never chose wears classic
	// green (owner decision, 2026-10-16): no "automatic" choice.
	homeColor = info.color || info.palette[0].hex;
	renderHomeSwatches();
	renderRecent();
}

// renderRecent lists the config's recent hosts under Join: a click puts
// the address in the (masked) box. Each shows its label and a masked
// address; the real one is only ever put in the box.
function renderRecent() {
	const hosts = info.recentHosts || [];
	$("recent").hidden = !hosts.length;
	$("recent-list").replaceChildren(...hosts.map((h) => el("li", {},
		el("button", {
			type: "button", class: "slotbtn", title: "Put this host in the address box",
			"aria-pressed": String($("addr").value.trim() === h.addr),
			onclick: () => { $("addr").value = h.addr; renderRecent(); $("join-btn").focus(); },
		}, h.label ? el("small", {}, h.label) : null, el("span", { class: "addr" }, info.showIPs ? h.addr : h.masked)))));
}

// applyShowIPs draws the header's switch and the Join box as info.showIPs
// says: on, the box shows its address and has no Show button; off, it is
// masked, revealed rows are masked again, and so are the log lines that
// came unmasked.
function applyShowIPs() {
	const on = !!info.showIPs;
	for (const sw of [$("showips"), $("set-showips")]) {
		sw.setAttribute("aria-checked", String(on));
		sw.title = on
			? "IP addresses are shown everywhere. Turn this off before you stream or share your screen."
			: "IP addresses are masked, so a stream never shows one. Turn this on to show them all.";
	}
	$("set-showips").lastElementChild.textContent = on ? "On" : "Off";
	$("addr-eye").hidden = on;
	setAddrShown(on);
	if (!on) {
		revealed = new Set();
		for (const li of $("log").children) {
			const m = unmasked.get(li);
			if (m) {
				li.lastChild.textContent = m.text;
				if (m.from !== undefined) li.firstChild.textContent = m.from + ": ";
				unmasked.delete(li);
			}
		}
	}
}

// onSettings redraws everything after the switch changed (here, or in
// another page of the same app): the views come masked or not from Go.
async function onSettings() {
	try {
		info = await call("Info");
	} catch {
		return;
	}
	applyShowIPs();
	renderRecent();
	renderLan();
	refreshLan();
	shared.session = 0; // the share card is read again by render
	await refresh();
}

// setAddrShown shows or masks the Join address box.
function setAddrShown(on) {
	$("addr").classList.toggle("secret", !on);
	const b = $("addr-eye");
	b.textContent = on ? "Hide" : "Show";
	b.setAttribute("aria-pressed", String(on));
}

function chip(text, cls = "", title = "") {
	return el("span", { class: "chip" + (cls ? " " + cls : ""), title: title || null }, text);
}

// The area chip's words (MemberView.area: from the pose positions).
const AREAS = { near: "Same area", far: "Far away" };

function memberRow(m, lobby) {
	const playing = lobby.phase !== "lobby";
	const swatch = el(m.self ? "button" : "span", {
		class: "dot", style: "--c:" + m.color, title: m.self ? "Change your tunic color (" + m.colorName + ")" : m.colorName,
		type: m.self ? "button" : null, "aria-label": m.self ? "Change your tunic color" : null,
		onclick: m.self ? () => { const s = $("lobby-swatches"); s.scrollIntoView({ block: "nearest", behavior: "smooth" }); s.querySelector("[aria-checked=true]")?.focus(); } : null,
	});
	const who = el("span", { class: "who" }, m.name, m.self ? el("small", {}, " (you)") : null);
	const crown = m.host ? el("span", { class: "crown", title: "Host" }) : null;
	if (crown) crown.innerHTML = CROWN;
	const chips = el("span", { class: "chips" },
		m.away ? chip("Reconnecting…", "away", "Their connection dropped; the host keeps their place for a minute") : null,
		chip(m.via === "cli" ? "CLI" : "App", m.via === "cli" ? "cli" : "app", m.via === "cli" ? "Playing from the command line" : "In the app"),
		m.rttMs > 0 && !m.away ? chip(m.rttMs + " ms", "rtt", "Round trip to the host") : null,
		m.dolphinLabel ? chip(m.dolphinLabel, "dolphin " + m.dolphin, "Their Dolphin") : null,
		playing && m.area ? chip(AREAS[m.area] || m.area, "area " + m.area, "About " + m.dist + " units from you") : null,
		// Co-op: the save file they'll play (their card's preview).
		!playing && m.file ? chip(m.file, "file", "Their save file for the co-op world") : null,
		// The host's Start says they're ready: their row has no ready chip.
		playing || m.host ? null : chip(m.ready ? "Ready ✓" : "Not ready", m.ready ? "ready" : "notready"),
	);
	const kick = m.canKick
		? el("button", { type: "button", class: "kick", title: "Remove " + m.name, onclick: () => confirmKick(m) }, "Kick")
		: null;
	return el("li", { class: (m.self ? "self" : "") + (m.away ? " away" : "") }, swatch, who, crown, chips, kick);
}

function render() {
	if (view !== "session" || !state) return;
	const st = state;
	const lobby = st.lobby && !st.lobby.legacy ? st.lobby : null;
	const ended = !st.running;

	renderSide();
	const ph = $("phase");
	ph.textContent = PHASES[st.phase] ?? st.phase;
	ph.className = "phase" + (st.phase === "lobby" ? " lobby" : st.phase === "in game" ? " ingame" : st.phase === "ended" ? " ended" : "");
	const playing = !!(lobby && lobby.phase !== "lobby");
	renderWhere(st, playing);
	$("room-title").textContent = roomTitle(st, lobby, playing);
	$("leave-btn").hidden = ended;

	$("ended").hidden = !(ended && !left) || !!(st.dialog && st.dialog.home);
	$("end-reason").textContent = st.failed ? st.endReason : "The session ended.";

	// The reconnect banner.
	const bn = st.banner;
	$("banner").hidden = !bn;
	if (bn) {
		$("banner-title").textContent = bn.title;
		$("banner-text").textContent = bn.text;
		$("banner-n").textContent = bn.attempts ? bn.attempts + (bn.attempts === 1 ? " try" : " tries") : "";
		const act = $("banner-act");
		act.hidden = !bn.action;
		act.textContent = bn.actionLabel || "";
		act.onclick = bn.action ? () => dialogAction(bn.action, null, st, bn.actionArg || "") : null;
		$("banner").querySelector(".spin").hidden = bn.kind === "coop" || (bn.kind === "dolphin" && !!bn.action);
	}

	// Members: the lobby's, or a direct session's players.
	const list = $("members");
	list.replaceChildren();
	if (lobby) {
		$("members-title").textContent = "Players";
		const n = lobby.members.length;
		$("members-count").textContent = n ? n + (n === 1 ? " player" : " players") : "";
		for (const m of lobby.members) list.append(memberRow(m, lobby));
		if (!lobby.members.length) {
			const n = lobby.link.attempts;
			list.append(el("li", { class: "empty" }, st.phase === "connecting"
				? "Reaching the host…" + (n ? " (" + n + (n === 1 ? " try)" : " tries)") : "")
				: "Nobody here yet."));
		}
	} else {
		$("members-title").textContent = "Players";
		$("members-count").textContent = "";
		list.append(el("li", {}, el("span", { class: "dot unknown" }), el("span", { class: "who" }, st.name, el("small", {}, " (you)"))));
		for (const p of st.players) {
			list.append(el("li", {},
				el("span", { class: "dot" + (p.color ? "" : " unknown"), style: p.color ? "--c:" + p.color : "" }),
				el("span", { class: "who" }, p.name)));
		}
	}

	// Your row's controls: color and Ready, in the lobby phase.
	const self = lobby ? lobby.members.find((m) => m.self) : null;
	const mine = $("mine");
	mine.hidden = !(self && !ended && lobby.phase === "lobby");
	if (!mine.hidden) {
		swatches($("lobby-swatches"), info.palette, self.color, (hex) => setColor(hex));
		const hex = $("lobby-hex");
		const custom = !info.palette.some((p) => p.hex === self.color);
		if (document.activeElement !== hex) hex.value = custom ? self.color : "";
		if (document.activeElement !== $("lobby-well")) $("lobby-well").value = self.color.toLowerCase();
		const rb = $("ready-btn");
		rb.hidden = !!lobby.host; // Start is the host's ready
		rb.setAttribute("aria-pressed", String(self.ready));
		rb.textContent = self.ready ? "Ready ✓" : "Ready";
		rb.title = self.ready ? "Click when you're not ready after all" : "Tell the host you're ready";
		$("self-dolphin").checked = !!st.startDolphinMyself;
	}

	// The host's panel, until Start.
	const hosting = !!(lobby && lobby.host && !ended && !playing);
	$("host-panel").hidden = !hosting;
	$("cols").classList.toggle("hosting", hosting);
	// In game: the minimap beside the players.
	const mapOn = playing && !ended && !!st.map;
	$("map-panel").hidden = !mapOn;
	$("cols").classList.toggle("ingame", mapOn);
	if (mapOn) drawMap(st.map);
	const home = hosting ? $("stack-left") : $("stack-right");
	if ($("feed").parentElement !== home) {
		home.append($("feed"));
		$("log").scrollTop = $("log").scrollHeight;
	}
	renderCoop(st, { hosting, playing, ended });
	if (hosting) {
		if (shared.session !== st.session) loadShare(st.session);
		const waiting = lobby.notReady || [];
		$("start-btn").disabled = !lobby.canStart || waiting.length > 0;
		$("start-anyway").hidden = !lobby.canStart || !waiting.length;
		$("start-note").textContent = waiting.length
			? "Waiting for " + waiting.join(", ") + " to be ready."
			: "Everyone's ready. Start opens Dolphin for every player with their patched disc.";
	}

	syncDialog(st);
}

// roomTitle is the Session page's title: whose session, and what it is
// (the phase pill and the line above it say the rest).
function roomTitle(st, lobby, playing) {
	if (!st.running) return "Session ended";
	if (!lobby) return st.role === "Host" ? "Your session" : "Joined session";
	if (lobby.host) return playing ? "Your session" : "Your lobby";
	const h = lobby.members.find((m) => m.host);
	if (!h) return st.phase === "connecting" ? "Reaching the host…" : "Lobby";
	return h.name + (/s$/i.test(h.name) ? "’ " : "’s ") + (playing ? "session" : "lobby");
}

// renderWhere is the header's line: who you are and where. In game:
// "You: ■ name · Hosting · 3 players · 24 ms" (design §2.7); IPs stay
// masked unless the switch is on.
function renderWhere(st, playing) {
	const w = $("where");
	if (!playing) {
		w.textContent = st.role === "Host"
			? "Hosting as " + st.name
			: "Joining " + ((info.showIPs ? st.addr : st.addrMasked) || "the host") + " as " + st.name;
		return;
	}
	const n = st.lobby ? st.lobby.members.length : 0;
	const bits = [st.role === "Host" ? "Hosting" : "Joined " + ((info.showIPs ? st.addr : st.addrMasked) || "the host"),
		n + (n === 1 ? " player" : " players")];
	if (st.latencyMs > 0) bits.push(st.latencyMs + " ms");
	w.replaceChildren("You: ", st.color ? el("i", { class: "me", style: "--c:" + st.color, title: "Your tunic" }) : "",
		st.name + " · " + bits.join(" · "));
}

// drawMap draws the minimap (the TUI's, as a sea chart): you in the
// middle, every other Link around you on the ground plane (+X right, +Z
// down, as the TUI draws it). The scale fits the farthest player; one
// off the chart sits on its edge. The dashed ring is "same area".
const SVGNS = "http://www.w3.org/2000/svg";
function svg(tag, attrs, text) {
	const n = document.createElementNS(SVGNS, tag);
	for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
	if (text !== undefined) n.textContent = text;
	return n;
}
function drawMap(m) {
	const kids = [svg("line", { class: "axis", x1: -100, y1: 0, x2: 100, y2: 0 }), svg("line", { class: "axis", x1: 0, y1: -100, x2: 0, y2: 100 })];
	let far = 0;
	for (const p of m.players) far = Math.max(far, Math.hypot(p.dx, p.dz));
	const range = Math.min(40000, Math.max(400, far * 1.4)); // world units from the center to the edge
	const k = 95 / range;
	if (m.units < range) kids.push(svg("circle", { class: "ring", cx: 0, cy: 0, r: m.units * k }));
	for (const p of m.players) {
		let x = p.dx * k, y = p.dz * k;
		const d = Math.hypot(x, y);
		if (d > 95) { x *= 95 / d; y *= 95 / d; }
		kids.push(svg("circle", { class: "dot", cx: x, cy: y, r: 7, fill: p.color || "#3c9e2e" }));
		kids.push(svg("text", { x: x + 10, y: y + 4 }, p.name));
	}
	kids.push(svg("circle", { class: "dot self", cx: 0, cy: 0, r: 8, fill: m.color || "#3c9e2e" }));
	kids.push(svg("text", { x: 0, y: 24, "text-anchor": "middle" }, "You"));
	$("map").replaceChildren(...kids);
	$("map-note").textContent = !m.selfOk
		? "Your position shows once your Link is in the world."
		: m.players.length
			? "The chart spans about " + Math.round(range).toLocaleString() + " units from you." +
				(m.units < range ? " Inside the dashed ring is the same area." : " Everyone on it is in your area.")
			: "Nobody else is in the world yet.";
}

// ---------- the host's share card ----------

async function loadShare(id) {
	const seq = ++shareSeq;
	shared = { session: id, port: 0, addrs: [] };
	let next = { session: id, port: 0, addrs: [] };
	try {
		const s = await call("ShareAddrs");
		next = { session: id, port: s.port, addrs: s.addrs || [] };
	} catch {
		// an empty card
	}
	if (seq !== shareSeq) return; // a newer call is on its way
	shared = next;
	renderShare();
}

function renderShare() {
	const ul = $("addrs");
	ul.replaceChildren(...shared.addrs.map((a) => {
		// Show reveals the address and its join code: the code holds the
		// address, so it is masked as one.
		const shown = revealed.has(a.addr);
		const raw = info.showIPs || shown;
		return el("li", { class: "addr " + a.kind },
			el("div", { class: "addrrow" },
				el("span", { class: "tag" }, a.label),
				el("code", {}, raw ? a.addr : a.masked),
				info.showIPs ? null : el("button", {
					type: "button", class: "btn small", "aria-pressed": String(shown),
					title: shown ? "Mask this address and its code again" : "Show this address and its code until you leave the screen",
					onclick: () => { if (shown) revealed.delete(a.addr); else revealed.add(a.addr); renderShare(); },
				}, shown ? "Hide" : "Show"),
				el("button", { type: "button", class: "btn small", onclick: () => copyText(a.addr, "the " + a.label + " address") }, "Copy")),
			a.code ? el("div", { class: "coderow" },
				el("span", { class: "codetag" }, "Join code"),
				el("code", {}, raw ? a.code : a.codeMasked),
				el("button", { type: "button", class: "btn small", onclick: () => copyText(a.code, "the " + a.label + " join code") }, "Copy code")) : null);
	}));
	if (!shared.addrs.length) ul.append(el("li", { class: "empty" }, "This computer has no network address a friend could reach."));
	const port = shared.port || 25565;
	$("share-hint").textContent = "A join code is the address, shortened: friends paste it in Join. " +
		"Friends on your network see this session under Join without either. " +
		"If Windows asks whether ww-multiplayer may use the network, allow it. " +
		"Over the internet: share a Radmin VPN network, or forward port " + port + ".";
}

// ---------- sessions on your network ----------

const LAN_PHASES = { starting: "Starting", "in game": "In game", unknown: "Unknown" };

// refreshLan reads the sessions heard on the network (Go listens while
// this is asked for: every second while Home shows).
async function refreshLan() {
	const seq = ++lanSeq;
	let v;
	try {
		v = await call("LanSessions");
	} catch {
		return;
	}
	if (seq !== lanSeq || view !== "home") return;
	if (v.showIPs && !info.showIPs) return; // from before the switch went off
	lan = v;
	renderLan();
}

// renderLan lists the sessions: the host's name, players, phase and
// version, the network it was heard on with its address masked (Show
// reveals it until Home is left), and Join. A session of another
// version is greyed and can't be joined.
function renderLan() {
	const list = $("lan-list");
	const rows = (lan.sessions || []).map((s) => {
		const key = "lan:" + s.id;
		const shown = revealed.has(key);
		const raw = info.showIPs || shown;
		const versionChip = s.compatible
			? (s.version && s.version !== info.version ? chip(s.version, "ver", "Another version that can still play with yours") : null)
			: chip(s.version || "other version", "ver bad", "Another version: both players need the same version");
		return el("li", { class: "lansess" + (s.compatible ? "" : " incompatible") },
			el("div", { class: "lanhead" },
				el("b", { class: "who" }, s.host || "Someone"),
				chip(s.players + (s.max ? "/" + s.max : "") + (s.players === 1 && !s.max ? " player" : " players"), "", "Players in the lobby"),
				LAN_PHASES[s.phase] ? chip(LAN_PHASES[s.phase], "area far", "They already pressed Start; you can still join") : null,
				s.coop ? chip("Co-op", "file", "The host chose a co-op world") : null,
				versionChip),
			el("div", { class: "lanaddr " + s.kind },
				el("span", { class: "tag" }, s.label),
				el("code", {}, raw ? s.addr : s.masked),
				info.showIPs ? null : el("button", {
					type: "button", class: "btn small", "aria-pressed": String(shown),
					title: shown ? "Mask this address again" : "Show this address until you leave Home",
					onclick: () => { if (shown) revealed.delete(key); else revealed.add(key); renderLan(); },
				}, shown ? "Hide" : "Show"),
				el("button", {
					type: "button", class: "btn small primary", disabled: !s.compatible || busy,
					title: s.compatible ? "Join " + (s.host || "this session") : "Both players need the same version",
					onclick: () => start(() => call("Join", { addr: s.addr, name: $("name").value, color: homeColor })),
				}, "Join")));
	});
	list.replaceChildren(...rows);
	if (!rows.length && lan.on) list.append(el("li", { class: "empty" }, "Looking for sessions on your network…"));
	$("lan-note").textContent = lan.note || (rows.length ? "" : "A friend's session shows here once they host on the same network (or Radmin VPN network).");
	$("lan").hidden = !lan.on && !lan.note;
}

// ---------- dialogs ----------

// syncDialog shows the dialog the state calls for (once per key), and
// closes a live one the state no longer has.
function syncDialog(st) {
	const d = st.dialog;
	const open = dialogKey();
	if (d && d.home && view === "session" && !closedDialogs.has(d.key)) {
		show("home"); // the session is over: its dialog opens over Home
	}
	if (d && !closedDialogs.has(d.key)) {
		const run = (id, arg) => dialogAction(id, d, st, arg);
		if (open === d.key) {
			// masked again, if the switch went off; a live check moves on
			setDialogText(d.text, d.detail || "", d.check ? dialogExtra(d, run) : undefined);
		} else {
			if (d.live) liveDialogs.add(d.key);
			openDialog({
				key: d.key, title: d.title, text: d.text, detail: d.detail || "",
				extra: dialogExtra(d, run),
				onClose: () => closedDialogs.add(d.key),
				// A question, or a "Remove the tag", takes no input for a
				// moment: the player may be typing in the chat.
				settle: !!d.cancel || d.actions.some((a) => a.id === "unbind"),
				// Escape on a co-op question is its no, as on the command line.
				onCancel: d.cancel ? () => { const [id, ...rest] = d.cancel.split(":"); run(id, rest.join(":")); } : null,
				actions: d.actions.map((a) => ({ label: a.label, primary: a.primary, run: () => run(a.id, a.arg || "") })),
			});
		}
	} else if (open && liveDialogs.has(open) && (!d || d.key !== open)) {
		closeDialog(); // reached the host after all, or Dolphin runs the right disc now
	}
}

async function dialogAction(id, d, st, arg = "") {
	if (await coopAction(id, arg, d)) {
		refresh();
		return;
	}
	switch (id) {
		case "downloads":
			if (mode() === "window") {
				try {
					await call("OpenDownloads");
				} catch (e) {
					toast(e.message, { bad: true });
				}
			} else {
				window.open(info.downloadsUrl, "_blank", "noopener");
			}
			break;
		case "copy-version":
			copyText(info.version);
			break;
		case "join-suggested":
			$("name").value = d.suggest;
			start(() => call("Join", { addr: st.addr, name: d.suggest, color: homeColor, ownerKey: $("owner-key").value }));
			break;
		case "change-name": {
			show("home");
			const taken = st.name ? "“" + st.name + "” is taken in that lobby." : "Pick another name.";
			$("name-error").textContent = d.suggest ? taken + " Try “" + d.suggest + "”." : taken;
			$("name").focus();
			$("name").select();
			break;
		}
		case "open-mods":
			beforeMods = "home"; // the session is over: Back goes Home
			show("mods");
			break;
		case "retry":
			try {
				await call("Retry");
				state = await call("State");
				if (unmaskedLate(state)) state = await call("State");
				session = state.session;
				render();
			} catch (e) {
				toast(e.message, { bad: true });
			}
			break;
		case "leave":
			leave();
			break;
		case "setup":
			openSetup();
			break;
		case "launch":
			try {
				await call("LaunchDolphin");
			} catch (e) {
				toast(e.message, { bad: true });
			}
			break;
	}
}

function confirmKick(m) {
	openDialog({
		title: "Remove " + m.name + "?",
		text: m.name + " leaves the session, and can't come back into it until everyone has left the lobby.",
		actions: [
			{ label: "Remove", primary: true, danger: true, run: async () => {
				try {
					await call("Kick", m.id);
				} catch (e) {
					toast(e.message, { bad: true });
				}
			} },
			{ label: "Cancel" },
		],
	});
}

// ---------- small actions ----------

// copyText copies text; what names it in the toast (an address is never
// printed there).
async function copyText(text, what = text) {
	try {
		if (mode() === "window") {
			await call("Copy", text);
		} else {
			await navigator.clipboard.writeText(text);
		}
		toast("Copied " + what);
	} catch (e) {
		toast("Couldn't copy: " + e.message, { bad: true });
	}
}

async function setColor(color) {
	try {
		await call("SetColor", color);
	} catch (e) {
		toast(e.message, { bad: true });
	}
}

// ---------- log ----------

// textLine is a log or chat line's text. A line that came unmasked (the
// switch was on when Go sent it) is drawn masked unless the switch is on
// now, and kept with its masked text when it is drawn unmasked.
function textLine(li, ev, from) {
	const raw = !!info.showIPs;
	li.append(raw || !ev.textMasked ? ev.text : ev.textMasked);
	if (raw && (ev.textMasked || ev.fromMasked)) {
		unmasked.set(li, { text: ev.textMasked || ev.text, from: from === undefined ? undefined : ev.fromMasked || ev.from });
	}
	return li;
}

function chatLine(ev, own = false) {
	const from = info.showIPs || !ev.fromMasked ? ev.from : ev.fromMasked;
	return textLine(el("li", { class: "chat" + (own ? " own" : "") }, el("b", {}, from + ": ")), ev, ev.from);
}

function addLine(node) {
	const log = $("log");
	const stick = log.scrollTop + log.clientHeight >= log.scrollHeight - 4;
	log.append(node);
	while (log.children.length > LOG_MAX) log.firstChild.remove();
	if (stick) log.scrollTop = log.scrollHeight;
}

// ---------- events ----------

// unmaskedLate says a state came unmasked though the switch is off now:
// its call left before the switch was turned off.
const unmaskedLate = (st) => st.showIPs && !info.showIPs;

async function refresh() {
	let st;
	try {
		st = await call("State");
	} catch {
		return;
	}
	if (unmaskedLate(st)) return; // the next refresh brings it masked
	state = st;
	if (view === "session" && state.session !== session && state.running) session = state.session;
	render();
	renderUpdate(); // the banner is back once the session is over (Mods too)
}

function onEv(ev) {
	if (ev.session && session && ev.session < session) return; // a late event from a session we left
	switch (ev.kind) {
		case "log":
			addLine(textLine(el("li", { class: (ev.level || "") + (ev.sync ? " sync" : "") }), ev));
			break;
		case "chat":
			addLine(chatLine(ev));
			break;
		case "settings":
			onSettings();
			if (view === "mods") refreshMods();
			break;
		case "mods":
			if (view === "mods") refreshMods();
			break;
		case "mod":
			// A mod's status changed (ev.status; its own events have
			// none): a stopped or failed mod says so wherever you are.
			if (ev.status && ev.status !== "LOADED" && ev.status !== "DISABLED") toast("Mod " + ev.mod + ": " + ev.text, { bad: true });
			if (view === "mods") refreshMods();
			break;
		case "state":
		case "ended":
		case "legacy-host":
			refresh();
			break;
		case "lobby-notice":
			if (!FATAL_NOTICES.has(ev.code)) toast(ev.text || ev.code, { bad: true });
			break;
		case "patch":
			onPatch(ev.patch);
			break;
		case "update":
			onUpdate(ev.update);
			break;
	}
}

// ---------- actions ----------

function setBusy(b) {
	busy = b;
	for (const id of ["host-btn", "join-btn"]) $(id).disabled = b;
	renderLan();
}

// start runs one of the forms' calls (Host or Join) and opens the
// Session screen.
async function start(run) {
	if (busy) return;
	$("home-error").textContent = "";
	$("name-error").textContent = "";
	setBusy(true);
	$("log").replaceChildren(); // before the call: the new session logs at once
	try {
		await run();
		left = false;
		state = await call("State");
		if (unmaskedLate(state)) state = await call("State");
		session = state.session;
		show("session");
	} catch (e) {
		$("home-error").textContent = e.message;
	} finally {
		setBusy(false);
	}
}

async function leave() {
	const b = $("leave-btn");
	b.disabled = true;
	left = true;
	try {
		await call("Leave");
	} finally {
		b.disabled = false;
		show("home");
	}
}

function wire() {
	initDialog();
	$("host-form").addEventListener("submit", (e) => {
		e.preventDefault();
		start(() => call("Host", { name: $("name").value, color: homeColor, port: Number($("port").value) || 0 }));
	});
	$("join-form").addEventListener("submit", (e) => {
		e.preventDefault();
		start(() => call("Join", { addr: $("addr").value, name: $("name").value, color: homeColor, ownerKey: $("owner-key").value }));
	});
	$("addr").addEventListener("input", renderRecent);
	$("addr-eye").addEventListener("click", () => setAddrShown($("addr").classList.contains("secret")));
	$("name").addEventListener("input", () => { $("name-error").textContent = ""; });
	$("leave-btn").addEventListener("click", leave);
	$("back-btn").addEventListener("click", () => show("home"));
	$("setup-btn").addEventListener("click", () => openSetup());
	$("mods-btn").addEventListener("click", () => {
		if (view !== "mods" && view !== "settings") beforeMods = view;
		show("mods");
	});
	$("mods-back").addEventListener("click", () => show(beforeMods === "session" && !(state && state.running) ? "home" : beforeMods));
	initMods({ openSetup });
	initUpdate({ view: () => view, running: () => !!(state && state.running), openSetup, onPatch });
	initTheme();
	const toggleIPs = async () => {
		try {
			await call("SetShowIPs", !info.showIPs);
			await onSettings(); // the "settings" event redraws too; this is for a late one
		} catch (e) {
			toast(e.message, { bad: true });
		}
	};
	$("showips").addEventListener("click", toggleIPs);
	$("set-showips").addEventListener("click", toggleIPs);
	$("nav-play").addEventListener("click", () => show(state && state.running ? "session" : "home"));
	$("nav-settings").addEventListener("click", () => show("settings"));
	$("version").addEventListener("click", () => show("settings"));
	$("name").addEventListener("input", renderSide);
	const startGame = async (anyway) => {
		$("start-btn").disabled = true;
		try {
			await call("Start", anyway);
		} catch (e) {
			toast(e.message, { bad: true });
		}
		refresh();
	};
	$("start-btn").addEventListener("click", () => startGame(false));
	$("start-anyway").addEventListener("click", () => {
		const waiting = (state && state.lobby && state.lobby.notReady) || [];
		openDialog({
			title: "Start anyway?",
			text: waiting.join(", ") + (waiting.length === 1 ? " isn't" : " aren't") + " ready yet. Start opens everyone's Dolphin now.",
			actions: [{ label: "Start", primary: true, run: () => startGame(true) }, { label: "Cancel" }],
		});
	});
	$("self-dolphin").addEventListener("change", async () => {
		try {
			await call("SetStartDolphinMyself", $("self-dolphin").checked);
		} catch (e) {
			toast(e.message, { bad: true });
		}
	});
	$("ready-btn").addEventListener("click", async () => {
		const pressed = $("ready-btn").getAttribute("aria-pressed") === "true";
		try {
			await call("SetReady", !pressed);
		} catch (e) {
			toast(e.message, { bad: true });
		}
	});
	$("lobby-well").addEventListener("change", () => {
		$("lobby-hex").value = $("lobby-well").value.toUpperCase();
		setColor($("lobby-well").value);
	});
	$("lobby-hex").addEventListener("change", () => {
		const v = $("lobby-hex").value.trim();
		if (v) setColor(v.startsWith("#") ? v : "#" + v);
	});
	// The feed's tabs: Chat (chat and the lines that matter), Co-op sync
	// (the co-op lines users care about: sync=true from Go) and All logs.
	for (const tab of $("feed-tabs").children) {
		tab.addEventListener("click", () => {
			const which = tab.dataset.tab;
			$("log").classList.toggle("chatonly", which === "chat");
			$("log").classList.toggle("synconly", which === "sync");
			for (const t of $("feed-tabs").children) t.setAttribute("aria-selected", String(t === tab));
			$("feed-title").textContent = { chat: "Chat", sync: "Co-op sync", all: "All logs" }[which];
			$("log").scrollTop = $("log").scrollHeight;
		});
	}
	initCoop({ copyText });
	$("chat-form").addEventListener("submit", async (e) => {
		e.preventDefault();
		const input = $("chat");
		const text = input.value;
		if (!text.trim()) return;
		try {
			// The line comes back as the chat shows it (masked or not).
			const line = await call("SendChat", text);
			input.value = "";
			// The host relays a line to everyone but its sender.
			if (state && state.running && line.text) addLine(chatLine(line, true));
		} catch (err) {
			toast(err.message, { bad: true });
		}
	});
}

async function main() {
	info = await call("Info");
	$("version").textContent = info.version;
	$("name").maxLength = info.maxName;
	$("port").value = info.port || 25565;
	applyShowIPs();
	fillHome();
	for (const w of info.warnings || []) toast(w, { bad: true });
	wire();
	onEvent(onEv);
	const setup = initSetup({
		info,
		// Setup opened from a running session goes back to it.
		show: (v) => show(v === "home" && state && state.running ? "session" : v),
		// The setup saved or finished: Home starts from what it saved.
		onSaved: async () => {
			info = await call("Info");
			fillHome();
		},
	});
	state = await call("State");
	if (state.running) {
		session = state.session;
		show("session");
	} else if ((await setup).needed) {
		openSetup();
	} else {
		show("home");
	}
	startUpdate();
	setInterval(() => {
		if (view === "session") refresh();
		else if (view === "home") refreshLan();
	}, 1000);
}

main().catch((e) => {
	document.body.append(el("p", { class: "error" }, "The app couldn't start its window: " + e.message));
});
