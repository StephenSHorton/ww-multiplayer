// app.js: the window's screens (App PR 5): Home (name, color, Host / Join)
// and Session (the lobby's members, colors and ready flags, or a direct
// session's players; the log; chat). Everything reaches Go through
// transport.js. The state comes whole from Bridge.State, re-read on every
// "state" event and once a second while a session runs, so there is no
// diffing here: each render rebuilds its lists.

import { call, onEvent } from "./transport.js";

const $ = (id) => document.getElementById(id);
const LOG_MAX = 500;

let info = null; // Bridge.Info
let state = null; // Bridge.State
let view = "home"; // "home" or "session"
let session = 0; // the session the Session screen shows
let homeColor = ""; // the Home form's color, a "#RRGGBB"
let left = false; // the user pressed Leave (no "ended" panel then)
let busy = false;

const PHASES = { connecting: "Connecting…", lobby: "Lobby", starting: "Starting", "in game": "In game", ended: "Ended", idle: "" };

function el(tag, props = {}, ...kids) {
	const n = document.createElement(tag);
	for (const [k, v] of Object.entries(props)) {
		if (k === "class") n.className = v;
		else if (k === "style") n.style.cssText = v;
		else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
		else if (v !== undefined && v !== null && v !== false) n.setAttribute(k, v === true ? "" : v);
	}
	for (const c of kids.flat()) if (c !== null && c !== undefined) n.append(c);
	return n;
}

function toast(text, { bad = false, actions = [] } = {}) {
	const t = el("div", { class: "toast" + (bad ? " bad" : "") }, el("div", {}, text));
	for (const [label, fn] of actions) {
		t.append(el("button", { class: "btn", type: "button", onclick: () => { t.remove(); fn(); } }, label));
	}
	$("toasts").append(t);
	setTimeout(() => t.remove(), actions.length ? 15000 : 7000);
}

// ---------- color swatches ----------

// swatches renders the palette into box. selected is a "#RRGGBB". Any
// color can be picked, whoever else wears it.
function swatches(box, selected, onPick, { disabled = false } = {}) {
	box.replaceChildren();
	for (const s of info.palette) {
		const label = s.name + (s.classic ? " (classic)" : "");
		box.append(el("button", {
			type: "button", class: "swatch", role: "radio", style: "--c:" + s.hex, title: label, "aria-label": label,
			"aria-checked": String(selected.toUpperCase() === s.hex), disabled,
			onclick: () => onPick(s.hex),
		}, el("i")));
	}
}

function renderHomeSwatches() {
	swatches($("home-swatches"), homeColor, (hex) => { homeColor = hex; renderHomeSwatches(); });
}

// ---------- screens ----------

function show(v) {
	view = v;
	$("home").hidden = v !== "home";
	$("session").hidden = v !== "session";
	if (v === "home") $("home-error").textContent = "";
	render();
}

function render() {
	if (view !== "session" || !state) return;
	const st = state;
	const lobby = st.lobby && !st.lobby.legacy ? st.lobby : null;
	const ended = !st.running;

	const ph = $("phase");
	ph.textContent = PHASES[st.phase] ?? st.phase;
	ph.className = "phase" + (st.phase === "lobby" ? " lobby" : st.phase === "in game" ? " ingame" : "");
	$("where").textContent = st.role === "Host"
		? "Hosting as " + st.name
		: "Joining " + st.addr + " as " + st.name;
	$("leave-btn").hidden = ended;

	$("ended").hidden = !(ended && !left);
	$("end-reason").textContent = st.failed ? st.endReason : "The session ended.";

	// Members: the lobby's, or a direct session's players.
	const list = $("members");
	list.replaceChildren();
	if (lobby) {
		$("members-title").textContent = "Lobby";
		for (const m of lobby.members) {
			list.append(el("li", {},
				el("span", { class: "dot", style: "--c:" + m.color, title: m.colorName }),
				el("span", { class: "who" }, m.name, m.self ? el("small", {}, " (you)") : null),
				m.host ? el("span", { class: "chip host" }, "host") : null,
				m.via === "cli" ? el("span", { class: "chip" }, "CLI") : null,
				m.away ? el("span", { class: "chip away" }, "away") : null,
				el("span", { class: "chip" + (m.ready ? " ready" : "") }, m.ready ? "ready" : "not ready"),
			));
		}
	} else {
		$("members-title").textContent = "Players";
		list.append(el("li", {}, el("span", { class: "dot unknown" }), el("span", { class: "who" }, st.name, el("small", {}, " (you)"))));
		for (const p of st.players) {
			list.append(el("li", {},
				el("span", { class: "dot" + (p.color ? "" : " unknown"), style: p.color ? "--c:" + p.color : "" }),
				el("span", { class: "who" }, p.name)));
		}
	}
	if (!list.children.length) list.append(el("li", { class: "empty" }, "Nobody here yet."));

	// Your row's controls: color and ready, in the lobby phase.
	const self = lobby ? lobby.members.find((m) => m.self) : null;
	const mine = $("mine");
	mine.hidden = !(self && !ended && lobby.phase === "lobby");
	if (!mine.hidden) {
		swatches($("lobby-swatches"), self.color, async (hex) => {
			try {
				await call("SetColor", hex);
			} catch (e) {
				toast(e.message, { bad: true });
			}
		});
		const rb = $("ready-btn");
		rb.setAttribute("aria-pressed", String(self.ready));
		rb.textContent = self.ready ? "Ready ✓" : "Ready";
	}

	const addrs = $("addrs");
	addrs.replaceChildren(...(st.hostAddrs || []).map((a) => el("li", {}, a)));
}

// ---------- log ----------

function addLine(node) {
	const log = $("log");
	const stick = log.scrollTop + log.clientHeight >= log.scrollHeight - 4;
	log.append(node);
	while (log.children.length > LOG_MAX) log.firstChild.remove();
	if (stick) log.scrollTop = log.scrollHeight;
}

// ---------- events ----------

async function refresh() {
	try {
		state = await call("State");
	} catch {
		return;
	}
	if (view === "session" && state.session !== session && state.running) session = state.session;
	render();
}

function onEv(ev) {
	if (ev.session && session && ev.session < session) return; // a late event from a session we left
	switch (ev.kind) {
		case "log":
			addLine(el("li", { class: ev.level || "" }, ev.text));
			break;
		case "chat":
			addLine(el("li", { class: "chat" }, el("b", {}, ev.from + ": "), ev.text));
			break;
		case "state":
		case "ended":
			refresh();
			break;
		case "lobby-notice": {
			const actions = [];
			if (ev.code === "name_taken" && ev.suggest) {
				actions.push(["Use “" + ev.suggest + "”", () => { $("name").value = ev.suggest; show("home"); }]);
			}
			toast(ev.text || ev.code, { bad: true, actions });
			break;
		}
		case "legacy-host":
			toast("This host runs an older ww-multiplayer without a lobby, so you joined directly.");
			break;
	}
}

// ---------- actions ----------

function setBusy(b) {
	busy = b;
	for (const id of ["host-btn", "join-btn"]) $(id).disabled = b;
}

// start runs one of the forms' calls (Host or Join) and opens the
// Session screen.
async function start(run) {
	if (busy) return;
	$("home-error").textContent = "";
	setBusy(true);
	$("log").replaceChildren(); // before the call: the new session logs at once
	try {
		await run();
		left = false;
		state = await call("State");
		session = state.session;
		show("session");
	} catch (e) {
		$("home-error").textContent = e.message;
	} finally {
		setBusy(false);
	}
}

function wire() {
	$("host-form").addEventListener("submit", (e) => {
		e.preventDefault();
		start(() => call("Host", { name: $("name").value, color: homeColor, port: Number($("port").value) || 0 }));
	});
	$("join-form").addEventListener("submit", (e) => {
		e.preventDefault();
		start(() => call("Join", { addr: $("addr").value, name: $("name").value, color: homeColor }));
	});
	$("leave-btn").addEventListener("click", async () => {
		const b = $("leave-btn");
		b.disabled = true;
		left = true;
		try {
			await call("Leave");
		} finally {
			b.disabled = false;
			show("home");
		}
	});
	$("back-btn").addEventListener("click", () => show("home"));
	$("ready-btn").addEventListener("click", async () => {
		const pressed = $("ready-btn").getAttribute("aria-pressed") === "true";
		try {
			await call("SetReady", !pressed);
		} catch (e) {
			toast(e.message, { bad: true });
		}
	});
	$("chat-form").addEventListener("submit", async (e) => {
		e.preventDefault();
		const input = $("chat");
		const text = input.value;
		if (!text.trim()) return;
		try {
			await call("SendChat", text);
			input.value = "";
		} catch (err) {
			toast(err.message, { bad: true });
		}
	});
}

async function main() {
	info = await call("Info");
	$("version").textContent = info.version;
	$("name").maxLength = info.maxName;
	$("name").value = info.name || "";
	$("port").value = info.port || 25565;
	$("recent-hosts").replaceChildren(...info.recentHosts.map((h) => el("option", { value: h.addr }, h.label || null)));
	homeColor = info.color || info.palette[0].hex; // no preference: classic green
	renderHomeSwatches();
	for (const w of info.warnings || []) toast(w, { bad: true });
	wire();
	onEvent(onEv);
	state = await call("State");
	if (state.running) {
		session = state.session;
		show("session");
	} else {
		show("home");
	}
	setInterval(() => { if (view === "session") refresh(); }, 1000);
}

main().catch((e) => {
	document.body.append(el("p", { class: "error" }, "The app couldn't start its window: " + e.message));
});
