// coop.js: co-op in the window (App PR 9, design §2.5, §2.7, §2.8): the
// host's world choice (Off, New world, Continue with the worlds on this
// computer, and Advanced), every player's save-file preview with the
// host's rules, the Session screen's co-op panel with its buttons, the
// progress card (what co-op shares, each dungeon's small keys), and the parts of the
// co-op dialogs Go describes (lists, option cards, a live check, the
// "Answer yes to co-op questions" box). Go decides what to show
// (internal/gui/coop.go); this draws it and sends the clicks.

import { call, mode } from "./transport.js";
import { $, el, toast } from "./ui.js";

let state = null;
let worlds = { session: 0, list: null, loading: false }; // the host's Continue list, read once per session
let advSession = 0; // the session whose Advanced fields were filled from the saved choice
const openDetails = new Set(); // the panel's open "Details" (redrawn every second)
let editing = false; // the host edits what co-op shares (the rules card's Edit)
let busy = false; // a rules change is on its way

// ---------- the host's world choice ----------

// adv is the Advanced fields' values.
function adv() {
	return {
		dryRun: $("coop-dry").checked, merge: $("coop-merge").checked,
		liveSwitches: $("coop-live").checked, iso: $("coop-iso").value.trim(),
	};
}

async function setCoop(modeName, world) {
	try {
		await call("SetCoop", Object.assign({ mode: modeName, world: world || "" }, adv()));
	} catch (e) {
		toast(e.message, { bad: true });
	}
}

async function loadWorlds(id) {
	if (worlds.loading) return;
	worlds = { session: id, list: worlds.session === id ? worlds.list : null, loading: true };
	try {
		worlds.list = (await call("CoopWorlds")) || [];
	} catch {
		worlds.list = [];
	}
	worlds.loading = false;
	if (state) renderHost(state);
}

// pickContinue selects Continue: the saved world when it is on disk,
// else the newest that can be loaded.
function pickContinue() {
	const list = (worlds.list || []).filter((w) => !w.err);
	if (!list.length) {
		toast("No co-op world on this computer yet: found one with New world.", { bad: true });
		return;
	}
	const saved = state && state.coop ? state.coop.choice.world : "";
	const w = list.find((x) => x.id === saved) || list[0];
	setCoop("continue", w.id);
}

function renderHost(st) {
	const c = st.coop;
	if (!c) return;
	if (advSession !== st.session) {
		advSession = st.session;
		$("coop-dry").checked = !!c.choice.dryRun;
		$("coop-merge").checked = !!c.choice.merge;
		$("coop-live").checked = !!c.choice.liveSwitches;
		$("coop-iso").value = c.choice.iso || "";
	}
	if (worlds.session !== st.session && !worlds.loading) loadWorlds(st.session);
	for (const b of $("coop-seg").children) b.setAttribute("aria-checked", String(b.dataset.mode === c.mode));
	if (c.mode !== "off" && document.activeElement && !$("coop-adv").contains(document.activeElement)) {
		$("coop-dry").checked = c.dryRun;
		$("coop-merge").checked = c.merge;
		$("coop-live").checked = c.liveSwitches;
	}
	$("coop-mode-hint").textContent = {
		off: "Off: you play together, each in your own world. Pick New world or Continue to share chests, keys, story and more.",
		new: "New world: after Start, load the file you want to share and stand in game; the world is founded from it.",
		continue: "Continue: everyone binds their file to this world, or plays a file already bound to it.",
	}[c.mode] || "";
	const ul = $("coop-worlds");
	ul.hidden = c.mode !== "continue";
	if (!ul.hidden) {
		const list = worlds.list || [];
		ul.replaceChildren(...list.map((w) => el("li", {},
			el("button", {
				type: "button", class: "slotbtn world", role: "radio", "aria-checked": String(w.id === c.world),
				disabled: w.err ? true : null, title: w.text,
				onclick: () => setCoop("continue", w.id),
			},
			el("code", {}, w.id),
			el("small", {}, w.err ? "can't be loaded" : "seq " + w.seq + (w.created ? " · founded " + w.created : "")),
			w.inUse ? el("span", { class: "chip warn", title: "Its session marker names a process that may still serve it" }, "in use") : null,
			w.stale ? el("span", { class: "chip", title: "Its last session did not end cleanly" }, "stale marker") : null))));
		if (!list.length) ul.append(el("li", { class: "empty" }, worlds.list ? "No world on this computer yet." : "Looking…"));
	}
}

// ---------- the save-file preview ----------

const BIND_CHIPS = {
	this: ["Bound here", "ready"], other: ["Other world", "warn"], unbound: ["Unbound", ""],
	stale: ["Stale tag", "warn"], newer: ["Can't join", "away"],
};

function choiceText(st) {
	const c = st.coop;
	const who = st.lobby && st.lobby.host ? "You chose" : "The host chose";
	const what = c.mode === "new" ? "a new world, founded from the host's file" : "to continue world " + c.world;
	const extra = [];
	if (c.dryRun) extra.push("dry run");
	if (c.merge) extra.push("merging allowed");
	if (c.liveSwitches) extra.push("live switches");
	return who + " " + what + (extra.length ? " (" + extra.join(", ") + ")" : "") + ".";
}

// rules are the host's co-op choice as rows a joiner can read at a
// glance (the host sets them in its own panel).
function rules(c) {
	const row = (label, value, on) => el("li", { class: on ? "on" : "" }, label, el("b", {}, value));
	return [
		row("World", c.mode === "new" ? "New, from the host's file" : (c.world || "").slice(0, 8), true),
		row("Dry run", c.dryRun ? "On" : "Off", c.dryRun),
		row("Merging", c.merge ? "Allowed" : "Off", c.merge),
		row("Live switches", c.liveSwitches ? "On" : "Off", c.liveSwitches),
	];
}

function renderCards(st, show) {
	const panel = $("card-panel");
	panel.hidden = !show;
	if (!show) return;
	const c = st.coop;
	$("card-choice").textContent = choiceText(st);
	$("card-rules").replaceChildren(...rules(c));
	const v = c.cards || { files: [], hint: "" };
	$("card-files").replaceChildren(...v.files.map((f) => {
		const [label, cls] = BIND_CHIPS[f.bind] || ["", ""];
		return el("li", { class: "file " + f.state },
			el("span", { class: "slotno" }, String(f.slot)),
			el("span", { class: "fname" }, f.state === "used" ? f.name : f.state === "empty" ? "New game" : "Corrupted"),
			f.state === "used" ? el("span", { class: "hearts", title: f.hearts + " heart containers" }, "♥ " + f.hearts) : null,
			label ? el("span", { class: "chip " + cls, title: f.bindText }, label) : null);
	}));
	$("card-hint").textContent = v.hint || "";
	$("card-note").textContent = v.err ? "Couldn't read your memory card: " + v.err : "Read from your memory card; your choice happens in game, when you load a file.";
	const joiner = !(st.lobby && st.lobby.host);
	$("my-dry-row").hidden = !joiner;
	if (joiner && document.activeElement !== $("my-dry")) $("my-dry").checked = !!c.myDryRun;
	// Every player's own relays, from the saved choice (redrawn every
	// second: a field being typed in keeps what is typed).
	const r = c.relays || { bossKill: "", refresh: "" };
	if (document.activeElement !== $("coop-bosskill")) $("coop-bosskill").value = r.bossKill || "";
	if (document.activeElement !== $("coop-refreshset")) $("coop-refreshset").value = r.refresh || "";
}

// setRelays saves this player's relays; a set the CLI would refuse is
// refused here in its words (--coop-boss-kill: ..., --coop-refresh: ...).
async function setRelays() {
	try {
		await call("SetCoopRelays", { bossKill: $("coop-bosskill").value.trim(), refresh: $("coop-refreshset").value.trim() });
	} catch (e) {
		toast(e.message, { bad: true });
	}
}

// ---------- the progress card (in game) ----------

// SHARES are the session features the progress card lists, in the order
// players think of them, with what each shares (proto.Feature names;
// "image" is the chests, pickups, visited rooms, dungeon items, charts and
// the rest of the shared image, docs/08 §3).
const SHARES = [
	["image", "World", "Chests, pickups, dungeon maps and compasses, charts and visited rooms"],
	["story", "Story", "Story progress, held back where a game isn't ready for it"],
	["inventory", "Items", "Items, songs, Triforce shards, pearls and bag sizes"],
	["switches", "Switches", "Switches that stay set: doors, gates, bridges"],
	["puzzle", "Puzzles", "Room and dungeon puzzles solved"],
	["combat", "Kills", "Enemies that stay defeated once one of you beats them"],
	["keys", "Small keys", "Each dungeon's small keys, counted from the chests and doors"],
	["life", "Hearts", "Max hearts, from every Heart Container and Piece of Heart the world has taken"],
	["magic", "Magic", "The magic meter"],
	["live-switches", "Live switches", "Switches of the room you stand in, while you are idle"],
];

// SHARES_NOTE is room.go's sharesNote, said at once while the host edits.
const SHARES_NOTE = "Small keys are counted from the doors the switches share: with Switches off, one key can open a door in each game.";

const KEY_ICON = '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="8" cy="9" r="5" fill="#ffd83a" stroke="#6b4b00" stroke-width="2"/><circle cx="8" cy="9" r="1.8" fill="#6b4b00"/><path d="M12 12l8 8M16 16l2-2M18.5 18.5l2-2" stroke="#6b4b00" stroke-width="2.4" stroke-linecap="round"/></svg>';

// pips draws n of total as small squares (total capped for room).
function pips(n, total) {
	const t = Math.min(total, 12);
	return el("span", { class: "bar2", "aria-hidden": "true" }, ...Array.from({ length: t }, (_, i) => el("i", { class: i < n ? "on" : "" })));
}

function keyRow(k) {
	const name = el("span", { class: "kname", title: k.dungeon ? k.dungeon : null }, k.name);
	if (!k.on) {
		return el("li", { class: "off" }, name, el("span", { class: "chip" }, "Not derived"),
			el("div", { class: "kmeta" }, k.why || "Its keys aren't derived."));
	}
	if (!k.seen) {
		return el("li", { class: "off" }, name, el("span", { class: "chip" }, "Not seen yet"),
			el("div", { class: "kmeta" }, "Counted once your file is in game."));
	}
	const count = el("span", { class: "kcount", title: "Small keys your game holds" }, k.have + (k.have === 1 ? " key" : " keys"));
	count.insertAdjacentHTML("afterbegin", KEY_ICON);
	const meta = [
		el("span", {}, pips(k.taken, k.sources), " " + k.taken + " of " + k.sources + " found"),
		el("span", {}, pips(k.open, k.doors), " " + k.open + " of " + k.doors + " doors open"),
	];
	if (k.pending) meta.push(el("span", {}, "→ " + k.target + " on its way"));
	else if (k.target !== k.have) meta.push(el("span", {}, "→ " + k.target + " once you're idle"));
	if (k.held) meta.push(el("span", { title: "Opened by your partner: the lock goes at your next room change" }, k.held + " held until your next room change"));
	return el("li", {}, name, count, el("div", { class: "kmeta" }, ...meta));
}

// renderProgress draws the progress card from the first pipeline's facts
// (this player's game): what the host shares, the story bits held back,
// each dungeon's small keys and max hearts. A dry run derives nothing.
function renderProgress(st, show) {
	const panel = $("progress-panel");
	const v = show && st.coop.panel.pipes.length ? st.coop.panel.pipes[0] : null;
	panel.hidden = !v;
	if (!v) return;
	const rules = st.coop.panel.rules;
	if (!(rules && rules.edit)) editing = false;
	const eb = $("rules-edit");
	eb.hidden = !(rules && rules.edit);
	eb.textContent = editing ? "Done" : "Edit";
	eb.classList.toggle("primary", editing);
	eb.title = editing ? "Lock what co-op shares again" : "Change what co-op shares, for every player, while you play";
	$("rules-editbar").hidden = !editing;
	panel.classList.toggle("editing", editing);
	const kids = [];
	if (v.dryRun) {
		kids.push(el("p", { class: "dim" }, "A dry run: co-op plans its writes and logs them, but shares nothing into your game."));
	} else if (editing && rules && rules.edit) {
		kids.push(ruleRows(rules));
		const on = new Set(rules.rows.filter((r) => r.on).map((r) => r.id));
		if (on.has("keys") && !on.has("switches")) kids.push(el("p", { class: "warnline" }, SHARES_NOTE));
	} else if (!(v.shares || []).length && !(rules && rules.host)) {
		kids.push(el("p", { class: "dim" }, "Nothing yet: the host grants what's shared once your file is bound to the world."));
	} else {
		// The host's chips are what its hub allows; a joiner's what its
		// game is granted.
		const rows = rules ? new Map(rules.rows.map((r) => [r.id, r])) : null;
		const on = rows ? new Set(rules.rows.filter((r) => r.on).map((r) => r.id)) : new Set(v.shares);
		kids.push(el("ul", { class: "shares" }, ...SHARES.filter(([id]) => id !== "live-switches" || on.has(id)).map(([id, label, what]) => {
			const miss = rows && rows.get(id) ? rows.get(id).missing || [] : [];
			const title = what + (on.has(id) ? "" : " (not shared in this session)") + (miss.length ? ". Not for " + miss.join(", ") + ": their stage tables differ from yours" : "");
			return el("li", { class: (on.has(id) ? "on" : "") + (miss.length ? " part" : ""), title }, el("i"), label);
		})));
		if (rules && !rules.host) kids.push(el("p", { class: "dim small" }, "The host chooses what co-op shares, and may change it while you play."));
		if (rules && rules.note) kids.push(el("p", { class: "warnline" }, rules.note));
	}
	const keys = (v.keys || []).filter((k) => k.dungeon);
	if (keys.length) {
		kids.push(el("div", { class: "subhead" }, "Dungeon keys"), el("ul", { class: "keys" }, ...keys.map(keyRow)));
	} else if (v.noKeyTable && !v.dryRun) {
		// As /status: "small keys: not derived (no co-op stage tables)".
		kids.push(el("div", { class: "subhead" }, "Dungeon keys"),
			el("p", { class: "okline no", id: "progress-nokeys" }, "Small keys aren't derived: there are no co-op stage tables for this disc"));
	}
	if (!v.dryRun) {
		kids.push(el("div", { class: "subhead" }, "Story and hearts"));
		const held = v.held || [];
		const shares = v.shares || [];
		kids.push(held.length
			? el("ul", { class: "heldlist", title: "Story bits your partner has that your game gets once it catches up" }, ...held.map((h) => el("li", {}, "Held back: " + h)))
			: shares.includes("story")
				? el("p", { class: "okline" }, "No story held back: your game has all of it")
				: el("p", { class: "okline no" }, shares.length ? "Story isn't shared in this session" : "Story isn't shared yet"));
		kids.push(el("p", { class: "okline" + (v.life ? "" : " no") }, v.life ? "Max hearts follow the hearts the world has taken" : "Max hearts aren't shared: " + (v.lifeWhy || "not granted")));
	}
	$("progress").replaceChildren(...kids);
}

// ruleRows is the rules in edit mode (the host's): a switch for each rule
// co-op can change while it runs, the others read-only with why.
function ruleRows(rules) {
	const feats = rules.rows.filter((r) => r.id !== "merge" && r.id !== "dry-run");
	return el("ul", { class: "rulelist" }, ...rules.rows.map((r) => {
		let ctl;
		if (r.edit) {
			ctl = el("button", {
				type: "button", class: "switch", id: "rule-" + r.id, role: "switch", "aria-checked": String(r.on), "aria-label": r.label, disabled: busy ? true : null,
				// The row turns at once: a second click before the next state
				// builds on this one, not on the state from before.
				onclick: () => {
					r.on = !r.on;
					if (state) renderProgress(state, true);
					return r.id === "merge" ? setMerge(r.on) : setShares(feats.filter((x) => x.on).map((x) => x.id));
				},
			}, el("i", { class: "knob", "aria-hidden": "true" }), el("span", {}, r.on ? "On" : "Off"));
		} else {
			ctl = el("span", { class: "chip", title: r.why || null }, r.id === "image" ? "Always on" : r.on ? "On · set at Start" : "Off · set at Start");
		}
		return el("li", { class: (r.on ? "on" : "") + (r.edit ? "" : " fixed") },
			el("div", { class: "rtext" },
				el("b", {}, r.label),
				el("small", {}, r.what),
				r.edit ? null : el("small", { class: "why" }, r.why),
				r.missing && r.missing.length ? el("small", { class: "why warn" }, "Not for " + r.missing.join(", ") + ": their stage tables differ from yours") : null),
			ctl);
	}));
}

async function setShares(on) {
	busy = true;
	try {
		await call("SetCoopShares", on);
	} catch (e) {
		toast(e.message, { bad: true });
	}
	busy = false;
}

async function setMerge(on) {
	busy = true;
	try {
		await call("SetMerge", on);
	} catch (e) {
		toast(e.message, { bad: true });
	}
	busy = false;
}

// ---------- the Room items card (in game) ----------

const MAGIC = { 0: "none", 16: "normal", 32: "double" };

// tile is one item slot: the world's best variant, or the slot dimmed.
function tile(x) {
	const name = x.have ? x.name.replace(/^the /, "") : x.slot;
	return el("li", { class: x.have ? "have" : "", title: x.have ? "The world has " + x.name : "Not in the world yet" }, name.charAt(0).toUpperCase() + name.slice(1));
}

// heartRow draws the world's max hearts, a quarter-filled last one for
// the pieces past them.
function heartRow(r) {
	const n = Math.min(r.maxHearts, 20);
	return el("span", { class: "hearts", "aria-hidden": "true" }, ...Array.from({ length: n }, () => el("i")), r.maxQuarters ? el("i", { class: "part" }) : null);
}

// renderRoom draws the Room items card from this game's view of the
// world (the first pipeline's): what every player's game takes from it.
function renderRoom(st, show) {
	const panel = $("room-panel");
	const r = show && st.coop.panel.pipes.length ? st.coop.panel.pipes[0].room : null;
	panel.hidden = !r;
	if (!r) return;
	const have = r.items.filter((x) => x.have).length;
	$("room-count").textContent = have + " of " + r.items.length + " items";
	const fact = (label, value) => el("span", { class: "fact" }, el("small", {}, label), value);
	const named = (xs) => xs.map((x) => el("li", { class: x.have ? "on" : "", title: x.have ? "The world has it" : "Not yet" }, el("i"), x.name));
	const kids = [];
	kids.push(el("div", { class: "equip" }, ...r.equip.map((x) => el("div", { class: "eq" + (x.have ? "" : " none") },
		el("small", {}, x.slot), el("b", {}, x.have ? x.name.replace(/^the /, "") : "None")))));
	if (r.hearts) {
		const max = r.maxHearts + ["", "¼", "½", "¾"][r.maxQuarters];
		kids.push(el("div", { class: "heartrow" }, heartRow(r),
			el("span", { class: "dim" }, max + " hearts max · " + r.containersTaken + " of " + r.containers + " containers · " + r.piecesTaken + " of " + r.pieces + " pieces" +
				(r.rewards ? " · " + r.given + " of " + r.rewards + " rewards" : ""))));
	} else {
		kids.push(el("p", { class: "okline no" }, "Hearts aren't counted: " + (r.heartsWhy || "no heart table")));
	}
	kids.push(el("div", { class: "subhead" }, "Item menu"), el("ul", { class: "slots" }, ...r.items.map(tile)));
	kids.push(el("div", { class: "subhead" }, "Songs and pearls"),
		el("ul", { class: "shares" }, ...named(r.songs), ...named(r.pearls)),
		el("div", { class: "facts" },
			fact("Triforce shards", r.shards + " of 8"),
			fact("Charts", r.charts + " of " + r.chartsTotal),
			r.salvaged ? fact("Treasure salvaged", String(r.salvaged)) : null,
			r.triforceCharts ? fact("Triforce charts deciphered", r.triforceCharts + " of 8") : null,
			fact("Wallet", r.wallet + " rupees"),
			fact("Quiver", r.quiver ? String(r.quiver) : "none"),
			fact("Bomb bag", r.bombBag ? String(r.bombBag) : "none"),
			fact("Magic", MAGIC[r.magic] || String(r.magic))),
		el("small", { class: "hint" }, "Read from the co-op world: every player's game takes these from it. Bottles, ammo, rupees, health and what the bait, spoils and delivery bags hold are each player's own."));
	$("room").replaceChildren(...kids);
}

// ---------- the co-op panel (in game) ----------

// more is a collapsed "Details" list that stays open across redraws.
function more(key, summary, lines) {
	const items = lines.filter((l) => l).map((l) => el("li", {}, l));
	if (!items.length) return null;
	const d = el("details", { class: "more", open: openDetails.has(key) ? true : null }, el("summary", {}, summary), el("ul", { class: "stats" }, ...items));
	d.addEventListener("toggle", () => { if (d.open) openDetails.add(key); else openDetails.delete(key); });
	return d;
}

function fact(label, value) {
	return el("span", { class: "fact" }, el("small", {}, label), value);
}

function renderPanel(st, show) {
	const panel = $("coop-panel");
	panel.hidden = !show;
	if (!show) return;
	const p = st.coop.panel;
	$("coop-pipes").replaceChildren(...(p.pipes.length ? p.pipes.map((v) => el("div", { class: "pipe" },
		el("div", { class: "headline " + v.tone }, el("i", { class: "lamp" }), v.headline),
		el("div", { class: "facts" },
			v.world ? fact("World", v.world.slice(0, 8) + "… · seq " + v.seq) : null,
			fact("Written", v.written + " bytes"),
			fact("Escapes", String(v.escapes)),
			fact("Verify failures", String(v.failed)),
			v.partner ? fact("Partner", v.partner) : null),
		more("pipe:" + v.player, "Progression, puzzles and combat", [...(v.progression || []), v.puzzle, v.combat, v.tuner]))) :
		[el("p", { class: "dim" }, "Co-op starts on your Dolphin once it runs the game.")]));
	const hub = p.hub;
	$("coop-hub").replaceChildren(...(hub ? [el("div", { class: "hub" },
		el("b", {}, "Your world " + hub.world + " · seq " + hub.seq + (hub.merge ? " · merging on" : "")),
		el("div", { class: "facts" }, ...hub.peers.map((x) => fact(x.name, x.state + (x.where ? " · " + x.where : "")))),
		more("hub", "The hub's puzzles and combat", hub.lines))] : []));
	const paused = p.pipes.some((v) => v.paused);
	const pb = $("coop-pause");
	pb.textContent = paused ? "Resume co-op writes" : "Pause co-op writes";
	pb.dataset.paused = String(paused);
	pb.title = paused ? "Write the world into your game again" : "Stop writing the world into your game; pose sync goes on";
	// Co-op v1.0 is two players (docs/08 §2): the warp goes to the one
	// other player, so there is no one to pick.
	$("coop-warp").disabled = p.partners.length === 0;
	if (document.activeElement !== $("coop-autoyes")) $("coop-autoyes").checked = !!st.coop.autoYes;
}

async function command(button) {
	try {
		await call("CoopCommand", button);
	} catch (e) {
		toast(e.message, { bad: true });
	}
}

// renderCoop draws the co-op parts of the Session screen for st.
export function renderCoop(st, { hosting, playing, ended }) {
	state = st;
	const c = st.coop;
	$("coop-host").hidden = !hosting || !c;
	if (hosting && c) renderHost(st);
	renderCards(st, !!(c && c.mode !== "off" && !ended && !playing));
	renderPanel(st, !!(c && c.panel && playing && !ended));
	renderProgress(st, !!(c && c.panel && playing && !ended));
	renderRoom(st, !!(c && c.panel && playing && !ended));
	$("tab-sync").hidden = !(c && c.mode !== "off");
}

// ---------- the co-op dialogs' extras ----------

// dialogExtra is the node under a dialog's text: its list, option
// cards, live check, note and auto-yes box; null for none.
export function dialogExtra(d, run) {
	const kids = [];
	if (d.check) kids.push(el("div", { class: "livecheck " + (d.check.ok ? "ok" : "wait") }, el("i", { class: "lamp" }), d.check.text));
	if (d.list && d.list.length) kids.push(el("ul", { class: "dlglist" }, ...d.list.map((l) => el("li", {}, l))));
	for (const c of d.cards || []) {
		kids.push(el("div", { class: "optcard" },
			el("b", {}, c.title),
			el("p", {}, c.text),
			c.steps ? el("ol", {}, ...c.steps.map((s) => el("li", {}, s))) : null,
			c.warn ? el("p", { class: "warnline" }, c.warn) : null,
			c.action ? el("button", { type: "button", class: "btn small", onclick: () => run(c.action, c.arg || "") }, c.label) : null));
	}
	if (d.note) {
		kids.push(el("p", { class: "dlgnote" }, d.note + " ",
			d.noteAction ? el("button", { type: "button", class: "linkbtn", onclick: () => run(d.noteAction.id, d.noteAction.arg || "") }, d.noteAction.label) : null));
	}
	if (d.autoYes) {
		const box = el("input", { type: "checkbox", checked: state && state.coop && state.coop.autoYes ? true : null });
		box.addEventListener("change", () => setAutoYes(box.checked));
		kids.push(el("label", { class: "check autoyes" }, box, el("span", {}, "Answer yes to co-op questions (this session)")));
	}
	return kids.length ? el("div", { class: "dlgextra" }, ...kids) : null;
}

async function setAutoYes(on) {
	try {
		await call("SetAutoYes", on);
	} catch (e) {
		toast(e.message, { bad: true });
	}
}

// coopAction runs a co-op dialog action; false for one it doesn't know.
export async function coopAction(id, arg, d) {
	const run = async (f, ok) => {
		try {
			await f();
			if (ok) toast(ok);
		} catch (e) {
			toast(e.message, { bad: true });
		}
	};
	switch (id) {
		case "answer-yes":
			await run(() => call("Answer", arg, true), d && d.kind === "bind" ? "Bound. Save in game so your card keeps the tag." : "");
			return true;
		case "answer-no":
			try {
				await call("Answer", arg, false);
			} catch {
				// answered already, or its session ended
			}
			return true;
		case "allow-merge":
			await run(() => call("AllowMerge", arg), "Merging is on for this session.");
			return true;
		case "dismiss-request":
			try {
				await call("DismissRequest", arg);
			} catch {
				// gone already
			}
			return true;
		case "dismiss-advice":
			try {
				await call("DismissAdvice", arg);
			} catch {
				// gone already
			}
			return true;
		case "request-merge":
			await run(() => call("RequestMerge"), "Asked the host to allow merging.");
			return true;
		case "set-merge":
			await run(() => call("SetMerge", true), "Merging is on for this session.");
			return true;
		case "unbind":
			await run(() => call("UnbindFile", arg), "Removed the file's co-op tag (in your game's memory).");
			return true;
		case "open-backups":
		case "open-userdir":
			if (mode() !== "window") {
				toast(id === "open-backups" && state && state.coop && state.coop.backup ? "The backup is in " + state.coop.backup : "Open the folder from your file manager.");
				return true;
			}
			await run(() => call("OpenFolder", id === "open-backups" ? "backups" : "userdir"));
			return true;
		case "coop-resume":
			await command("resume");
			return true;
	}
	return false;
}

// ---------- wiring ----------

export function initCoop({ copyText }) {
	for (const b of $("coop-seg").children) {
		b.addEventListener("click", () => {
			const m = b.dataset.mode;
			if (m === "continue") {
				if (!worlds.list) {
					loadWorlds(state ? state.session : 0).then(pickContinue);
				} else {
					pickContinue();
				}
				return;
			}
			setCoop(m);
		});
	}
	for (const id of ["coop-dry", "coop-merge", "coop-live", "coop-iso"]) {
		$(id).addEventListener("change", () => {
			if (!state || !state.coop) return;
			setCoop(state.coop.mode, state.coop.mode === "continue" ? state.coop.world : "");
		});
	}
	$("my-dry").addEventListener("change", () => call("SetMyCoopDryRun", $("my-dry").checked).catch((e) => toast(e.message, { bad: true })));
	for (const id of ["coop-bosskill", "coop-refreshset"]) $(id).addEventListener("change", setRelays);
	$("rules-edit").addEventListener("click", () => {
		editing = !editing;
		if (state) renderProgress(state, true);
	});
	$("coop-refresh").addEventListener("click", () => command("refresh"));
	$("coop-warp").addEventListener("click", () => command("warp"));
	$("coop-pause").addEventListener("click", () => command($("coop-pause").dataset.paused === "true" ? "resume" : "pause"));
	$("coop-copy").addEventListener("click", async () => {
		try {
			copyText(await call("CoopStatusText"), "co-op's status");
		} catch (e) {
			toast(e.message, { bad: true });
		}
	});
	$("coop-autoyes").addEventListener("change", () => setAutoYes($("coop-autoyes").checked));
}
