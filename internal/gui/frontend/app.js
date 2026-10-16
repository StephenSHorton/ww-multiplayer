// app.js: the window's screens: Home (name, color, Host / Join, recent
// hosts) and Session, which is the Lobby (App PR 7: members with their
// colors, ready and connection, your tunic and Ready, the host's panel
// with the share card, co-op and Start, chat, Kick) or a direct session's
// players. Everything reaches Go through transport.js. The state comes
// whole from Bridge.State, re-read on every "state" event and once a
// second while a session runs, so there is no diffing here: each render
// rebuilds its lists. The dialogs a join error opens and the reconnect
// banner come in the state too (Go decides them: internal/gui/lobby.go).

import { call, onEvent, mode } from "./transport.js";
import { $, el, toast, swatches, openDialog, closeDialog, dialogKey, setDialogText, initDialog } from "./ui.js";
import { initSetup, openSetup, onPatch } from "./setup.js";

const LOG_MAX = 500;

let info = null; // Bridge.Info
let state = null; // Bridge.State
let view = "home"; // "home", "session" or "setup" (setup.js)
let session = 0; // the session the Session screen shows
let homeColor = ""; // the Home form's color, a "#RRGGBB"
let left = false; // the user pressed Leave (no "ended" panel then)
let busy = false;
let shared = { session: 0, port: 0, addrs: [] }; // the host's share card, read once per session
let shareSeq = 0; // the newest ShareAddrs call: an older reply is dropped
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

const PHASES = { connecting: "Connecting…", lobby: "Lobby", starting: "Starting", "in game": "In game", ended: "Ended", idle: "" };
// Notices that end the session: its dialog says it, not a toast.
const FATAL_NOTICES = new Set(["name_taken", "full", "kicked", "replaced"]);

// A crown for the host's row (drawn here, no game assets).
const CROWN = '<svg viewBox="0 0 24 18" aria-hidden="true"><path d="M2 16 1 4l6 5 5-8 5 8 6-5-1 12Z" fill="#ffe63a" stroke="#142408" stroke-width="2" stroke-linejoin="round"/><circle cx="12" cy="11" r="1.8" fill="#e0302a"/></svg>';

// ---------- color swatches ----------

function renderHomeSwatches() {
	swatches($("home-swatches"), info.palette, homeColor, (hex) => { homeColor = hex; renderHomeSwatches(); });
}

// ---------- screens ----------

function show(v) {
	if (v !== view) {
		revealed = new Set();
		setAddrShown(info.showIPs);
	}
	view = v;
	$("home").hidden = v !== "home";
	$("session").hidden = v !== "session";
	$("setup").hidden = v !== "setup";
	$("setup-btn").hidden = v === "session";
	$("bar").hidden = v !== "session";
	if (v !== "session") $("leave-btn").hidden = true;
	if (v === "home") {
		$("home-error").textContent = "";
		renderRecent();
		// A join that reached its host saved it as a recent host meanwhile.
		call("Info").then((i) => { info = i; renderRecent(); }).catch(() => {});
	}
	render();
}

// fillHome puts the saved name and color into the Home form (at start, and
// after the setup saved them).
function fillHome() {
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
	const sw = $("showips");
	sw.setAttribute("aria-checked", String(on));
	sw.title = on
		? "IP addresses are shown everywhere. Turn this off before you stream or share your screen."
		: "IP addresses are masked, so a stream never shows one. Turn this on to show them all.";
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

function memberRow(m, lobby) {
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
		chip(m.ready ? "Ready ✓" : "Not ready", m.ready ? "ready" : "notready"),
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

	const ph = $("phase");
	ph.textContent = PHASES[st.phase] ?? st.phase;
	ph.className = "phase" + (st.phase === "lobby" ? " lobby" : st.phase === "in game" ? " ingame" : "");
	$("where").textContent = st.role === "Host"
		? "Hosting as " + st.name
		: "Joining " + ((info.showIPs ? st.addr : st.addrMasked) || "the host") + " as " + st.name;
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
	}

	// Members: the lobby's, or a direct session's players.
	const list = $("members");
	list.replaceChildren();
	if (lobby) {
		$("members-title").textContent = "Lobby";
		for (const m of lobby.members) list.append(memberRow(m, lobby));
		if (!lobby.members.length) {
			const n = lobby.link.attempts;
			list.append(el("li", { class: "empty" }, st.phase === "connecting"
				? "Reaching the host…" + (n ? " (" + n + (n === 1 ? " try)" : " tries)") : "")
				: "Nobody here yet."));
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
		rb.setAttribute("aria-pressed", String(self.ready));
		rb.textContent = self.ready ? "Ready ✓" : "Ready";
		rb.title = self.ready ? "Click when you're not ready after all" : "Tell the host you're ready";
	}

	// The host's panel.
	const hosting = !!(lobby && lobby.host && !ended);
	$("host-panel").hidden = !hosting;
	$("cols").classList.toggle("hosting", hosting);
	const home = hosting ? $("stack-left") : $("stack-right");
	if ($("feed").parentElement !== home) {
		home.append($("feed"));
		$("log").scrollTop = $("log").scrollHeight;
	}
	if (hosting) {
		if (shared.session !== st.session) loadShare(st.session);
		$("start-btn").disabled = !lobby.canStart;
	}

	syncDialog(st);
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
		const shown = revealed.has(a.addr);
		return el("li", { class: "addr " + a.kind },
			el("span", { class: "tag" }, a.label),
			el("code", {}, info.showIPs || shown ? a.addr : a.masked),
			info.showIPs ? null : el("button", {
				type: "button", class: "btn small", "aria-pressed": String(shown),
				title: shown ? "Mask this address again" : "Show this address until you leave the screen",
				onclick: () => { if (shown) revealed.delete(a.addr); else revealed.add(a.addr); renderShare(); },
			}, shown ? "Hide" : "Show"),
			el("button", { type: "button", class: "btn small", onclick: () => copyText(a.addr, "the " + a.label + " address") }, "Copy"));
	}));
	if (!shared.addrs.length) ul.append(el("li", { class: "empty" }, "This computer has no network address a friend could reach."));
	const port = shared.port || 25565;
	$("share-hint").textContent = "If Windows asks whether ww-multiplayer may use the network, allow it. " +
		"Over the internet: share a Radmin VPN network, or forward port " + port + ".";
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
		if (open === d.key) {
			setDialogText(d.text, d.detail || ""); // masked again, if the switch went off
		} else {
			openDialog({
				key: d.key, title: d.title, text: d.text, detail: d.detail || "",
				onClose: () => closedDialogs.add(d.key),
				actions: d.actions.map((a) => ({ label: a.label, primary: a.primary, run: () => dialogAction(a.id, d, st) })),
			});
		}
	} else if (open && open.endsWith(":unreachable") && (!d || d.key !== open)) {
		closeDialog(); // reached the host after all
	}
}

async function dialogAction(id, d, st) {
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
			start(() => call("Join", { addr: st.addr, name: d.suggest, color: homeColor }));
			break;
		case "change-name": {
			show("home");
			const taken = st.name ? "“" + st.name + "” is taken in that lobby." : "Pick another name.";
			$("name-error").textContent = d.suggest ? taken + " Try “" + d.suggest + "”." : taken;
			$("name").focus();
			$("name").select();
			break;
		}
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
}

function onEv(ev) {
	if (ev.session && session && ev.session < session) return; // a late event from a session we left
	switch (ev.kind) {
		case "log":
			addLine(textLine(el("li", { class: ev.level || "" }), ev));
			break;
		case "chat":
			addLine(chatLine(ev));
			break;
		case "settings":
			onSettings();
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
		start(() => call("Join", { addr: $("addr").value, name: $("name").value, color: homeColor }));
	});
	$("addr").addEventListener("input", renderRecent);
	$("addr-eye").addEventListener("click", () => setAddrShown($("addr").classList.contains("secret")));
	$("name").addEventListener("input", () => { $("name-error").textContent = ""; });
	$("leave-btn").addEventListener("click", leave);
	$("back-btn").addEventListener("click", () => show("home"));
	$("setup-btn").addEventListener("click", () => openSetup());
	$("showips").addEventListener("click", async () => {
		try {
			await call("SetShowIPs", !info.showIPs);
			await onSettings(); // the "settings" event redraws too; this is for a late one
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
	$("log-toggle").addEventListener("click", () => {
		const all = $("log").classList.toggle("chatonly") === false;
		$("log-toggle").setAttribute("aria-pressed", String(all));
		$("log").scrollTop = $("log").scrollHeight;
	});
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
		info, show,
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
	setInterval(() => { if (view === "session") refresh(); }, 1000);
}

main().catch((e) => {
	document.body.append(el("p", { class: "error" }, "The app couldn't start its window: " + e.message));
});
