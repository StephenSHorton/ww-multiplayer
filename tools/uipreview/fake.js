// fake.js: a fake Bridge for tools/uipreview (window.go.gui.Bridge and
// window.runtime, as Wails binds them), answering from the scenario the
// page's ?s= names. The shapes are internal/gui's views (view.go, coop.go,
// lobby.go, setup.go, mods.go, update.go, lan.go); the values are made up.
// Scenarios: home, home-update, home-empty, setup, setup-game,
// setup-dolphin, mods, lobby-host, lobby-join, ingame, ingame-host,
// reconnecting, dialog, ended. ?theme=dark|light sets the theme.
// Variants (main_test.go's DOM checks use them):
//
//	&legacy=1        a pre-lobby host: a legacy lobby, no members, players
//	&shares=none     the progress card before any feature is granted
//	&shares=nostory  a host that shares no story
//	&nokeys=1        no co-op stage tables (NoKeyTable)
//	&tables=differ   ingame-host: Blake's stage tables differ (no switches, puzzles, kills, keys, hearts for him)
//	&room=none       the Room items card before the hub's world came in
//	&two=1           two players (no Sam): Warp to on the partner's row
//	&upd=1           a newer release is out, in any scenario
//	&click=a,b       click these element ids in turn once the page is up
(function () {
	const q = new URLSearchParams(location.search);
	const S = q.get("s") || "home";
	const theme = q.get("theme");
	const legacy = q.get("legacy") === "1";
	const shareMode = q.get("shares") || "";
	const noKeys = q.get("nokeys") === "1";
	const tablesDiffer = q.get("tables") === "differ";
	const noRoom = q.get("room") === "none";
	const two = q.get("two") === "1";
	const updOut = S === "home-update" || q.get("upd") === "1";
	try {
		if (theme) localStorage.setItem("ww-theme", theme);
		else localStorage.removeItem("ww-theme");
	} catch {}

	const palette = [
		["green", "#3C9A2E", true], ["red", "#D02C2C"], ["blue", "#2C5CE0"], ["purple", "#8C3CD0"],
		["orange", "#F08C1E"], ["cyan", "#1EC0D0"], ["pink", "#F06EB4"], ["black", "#2C2C34"],
	].map(([name, hex, classic]) => ({ name, hex, classic: !!classic }));

	const info = {
		version: "v0.5.2", os: "windows", palette, name: S === "setup" ? "" : "Stephen", color: "#2C5CE0", port: 25565,
		recentHosts: S === "home-empty" ? [] : [
			{ addr: "192.168.1.42", masked: "•••.•••.•••.42", label: "Blake's PC" },
			{ addr: "26.12.4.9", masked: "••.••.•.9", label: "Radmin VPN" },
		],
		maxName: 16, showIPs: false, downloadsUrl: "https://github.com/StephenSHorton/ww-multiplayer/releases",
	};

	const setupView = {
		needed: S === "setup", done: S !== "setup",
		gaps: S === "setup" ? [{ step: "profile", text: "No name yet" }, { step: "game", text: "No patched disc" }] : [],
		name: info.name, color: info.color,
		patched: "C:\\Games\\Wind Waker-multiplayer.iso", patchedOK: S !== "setup",
		vanilla: "C:\\Games\\Wind Waker.ciso", vanillaOK: true,
		dolphinExe: "C:\\Users\\you\\Desktop\\Dolphin-x64\\Dolphin.exe", dolphinOK: S !== "setup",
		userDir: "", fileDialog: true, env: [],
		patch: { id: 0, seq: 0, stage: "idle", running: false, percent: 0, in: "", out: "", image: "", alreadyPatched: false, coop: { state: "", text: "" }, lines: [], error: "" },
		mac: null,
	};
	const patchDone = {
		id: 1, seq: 9, stage: "done", running: false, percent: 100,
		in: "C:\\Games\\Wind Waker.ciso", out: "C:\\Games\\Wind Waker-multiplayer.iso", image: "C:\\Games\\Wind Waker-multiplayer.iso",
		alreadyPatched: false, coop: { state: "ok", text: "1 418 stages, 6 key tables, 50 heart sources" },
		lines: ["reading C:\\Games\\Wind Waker.ciso (CISO)", "DOL: T2 spliced at 0x80410000", "RELS.arc: the mod's REL placed", "wrote C:\\Games\\Wind Waker-multiplayer.iso"], error: "",
	};

	const dolphinView = {
		exe: "C:\\Users\\you\\Desktop\\Dolphin-x64\\Dolphin.exe", source: "portable", found: true, userDir: "C:\\Users\\you\\Desktop\\Dolphin-x64\\User", portable: true,
		candidates: [
			{ exe: "C:\\Users\\you\\Desktop\\Dolphin-x64\\Dolphin.exe", source: "portable", found: true },
			{ exe: "C:\\Program Files\\Dolphin\\Dolphin.exe", source: "installed", found: true },
		],
		saved: "", savedOK: false, savedUserDir: "", mac: null,
	};

	const mods = {
		dev: false, showIPs: false,
		mods: [
			{ id: "multiplayer", name: "Multiplayer", version: "0.5.2", description: "See your friends' Links in your game: poses, tunic colors, the lobby and Start.", status: "LOADED", statusText: "Running", reason: "", missing: [], dev: false, requires: [], net: true, enabled: true, saved: true, pending: false, flag: "", verbs: [] },
			{ id: "coop", name: "Co-op", version: "0.5.2", description: "Share one world: chests, keys, story, items and bosses.", status: "LOADED", statusText: "Running", reason: "", missing: [], dev: false, requires: ["multiplayer"], net: false, enabled: true, saved: true, pending: false, flag: "", verbs: [] },
		],
	};

	const update = {
		seq: 1, enabled: true, dev: false, current: "v0.5.2", latest: updOut ? "v0.6.0" : "v0.5.2",
		available: updOut, notesURL: "", canInstall: true, stage: updOut ? "available" : "idle", percent: 0, error: "", installed: "", repatch: "",
	};

	const lan = S === "home-empty" ? { on: true, sessions: [], showIPs: false } : {
		on: true, showIPs: false, sessions: [
			{ id: "a1", host: "Blake", addr: "192.168.1.42", masked: "•••.•••.•••.42", kind: "lan", label: "LAN", players: 2, max: 3, phase: "", coop: true, version: "v0.5.2", compatible: true },
			{ id: "b2", host: "Sam", addr: "192.168.1.77", masked: "•••.•••.•••.77", kind: "lan", label: "LAN", players: 1, max: 3, phase: "in game", coop: false, version: "v0.4.1", compatible: false },
		],
	};

	const share = {
		port: 25565, addrs: [
			{ addr: "192.168.1.20", masked: "•••.•••.•••.20", kind: "lan", label: "LAN", code: "WW-30AG-09AX", codeMasked: "WW-••••-••AX" },
			{ addr: "26.44.120.3", masked: "••.••.•••.3", kind: "radmin", label: "Radmin VPN", code: "WW-1Q2W-3E4R", codeMasked: "WW-••••-••4R" },
		],
	};

	const worlds = [
		{ id: "7f3a91c2d04e4b51", seq: 412, created: "2026-10-19 21:04", inUse: false, stale: false, text: "" },
		{ id: "0b88e1f05a7c3d29", seq: 37, created: "2026-10-12 18:40", inUse: false, stale: true, text: "" },
	];

	const host = S === "lobby-host" || S === "ingame-host";
	const playing = S.startsWith("ingame") || S === "reconnecting";
	const phase = playing ? "in game" : "lobby";
	// "ended": a lobby that ends a moment after the page opens (the
	// Session page's ended card needs a session that was running).
	let isOver = false;
	const over = () => isOver;
	const lobbyPhase = playing ? "in game" : "lobby";
	const dolphinChip = playing ? { dolphin: "in_game", dolphinLabel: "In game" } : { dolphin: "", dolphinLabel: "" };
	// In game, co-op's row facts (room.go memberCoop): the host's hub knows
	// every peer, a joiner only where its partner is (with one other
	// player); Warp to is on the one other player's row (&two=1: no Sam).
	const coopRow = (name) => !playing ? {} : host
		? { coop: name === "Sam" ? "" : "Bound · DRC room 3" }
		: { coop: two && name === "Blake" ? "DRC room 3" : "" };
	const members = [
		{ id: 1, name: host ? "Stephen" : "Blake", color: host ? "#2C5CE0" : "#F08C1E", colorName: host ? "blue" : "orange", ready: true, host: true, self: host, via: "app", away: false, ...dolphinChip, area: playing && !host ? "near" : "", dist: 140, rttMs: 0, file: playing ? "" : "File 1 “Link”: bound", canKick: false },
		{ id: 2, name: host ? "Blake" : "Stephen", color: host ? "#F08C1E" : "#2C5CE0", colorName: host ? "orange" : "blue", ready: !host, host: false, self: !host, via: "app", away: S === "reconnecting", ...dolphinChip, area: playing && host ? "near" : "", dist: 140, rttMs: 24, file: playing ? "" : "File 2 “Stephen”: unbound", canKick: host },
		{ id: 3, name: "Sam", color: "#3C9A2E", colorName: "green", ready: false, host: false, self: false, via: "cli", away: false, dolphin: playing ? "title" : "", dolphinLabel: playing ? "Title screen" : "", area: playing ? "far" : "", dist: 5400, rttMs: 61, file: "", canKick: host },
	];
	if (two) members.pop();
	for (const m of members) Object.assign(m, coopRow(m.name), { canWarp: playing && two && !m.self });

	// The rules (room.go rulesView) and the Room items card (roomView).
	const RULES = [
		["image", "World", "Chests, pickups, dungeon maps and compasses, charts and visited rooms", "It is the shared world itself, and every other rule hangs on it. Each player's Pause co-op writes stops it for their game."],
		["story", "Story", "Story progress, held back where a game isn't ready for it"],
		["inventory", "Items", "Items, songs, Triforce shards, pearls and bag sizes"],
		["switches", "Switches", "Switches that stay set: doors, gates, bridges"],
		["puzzle", "Puzzles", "Room and dungeon puzzles solved"],
		["combat", "Kills", "Enemies that stay defeated once one of you beats them"],
		["keys", "Small keys", "Each dungeon's small keys, counted from the chests and doors"],
		["life", "Hearts", "Max hearts, from every Heart Container and Piece of Heart the world has taken"],
		["magic", "Magic", "The magic meter"],
		["live-switches", "Live switches", "Switches of the room you stand in, while you are idle"],
		["merge", "Merging", "A file with progress the world lacks may still bind, and its progress joins the world"],
		["dry-run", "Dry run", "Co-op plans its writes and logs them, but writes nothing into the games", "Each game's co-op writer is set up when co-op starts: choose it before Start."],
	];
	const tableFeats = new Set(["switches", "live-switches", "puzzle", "combat", "keys", "life"]);
	function rulesOf(on) {
		const rows = RULES.map(([id, label, what, why]) => ({
			id, label, what, on: on.has(id), edit: host && !why, why: host ? why || "" : "",
			missing: host && tablesDiffer && on.has(id) && tableFeats.has(id) ? ["Blake"] : [],
		}));
		return { host, edit: host, rows, note: on.has("keys") && !on.has("switches") ? "Small keys are counted from the doors the switches share: with Switches off, one key can open a door in each game." : "" };
	}
	const slots = [
		["telescope", "the telescope"], ["sail", "the sail"], ["Wind Waker", "the Wind Waker"], ["grappling hook", "the grappling hook"],
		["spoils bag", "the spoils bag"], ["boomerang", "the boomerang"], ["Deku Leaf", "the Deku Leaf"], ["Tingle Tuner", ""],
		["picto box", "the picto box"], ["iron boots", ""], ["magic armor", ""], ["bait bag", "the bait bag"],
		["bow", ""], ["bombs", ""], ["delivery bag", "the delivery bag"], ["hookshot", ""], ["Skull Hammer", ""],
	];
	const room = {
		items: slots.map(([slot, name]) => ({ slot, name, have: !!name })),
		equip: [{ slot: "sword", name: "the Hero's Sword", have: true }, { slot: "shield", name: "the Hero's Shield", have: true }, { slot: "bracelets", name: "", have: false }],
		songs: [["Wind's Requiem", true], ["Ballad of Gales", true], ["Command Melody", true], ["Earth God's Lyric", false], ["Wind God's Aria", false], ["Song of Passing", false]].map(([name, have]) => ({ name, have })),
		pearls: [["Nayru's Pearl", false], ["Din's Pearl", true], ["Farore's Pearl", false]].map(([name, have]) => ({ name, have })),
		shards: 0, charts: 7, chartsTotal: 49, salvaged: 2, triforceCharts: 0,
		wallet: 1000, quiver: 0, bombBag: 0, magic: 16,
		hearts: true, containers: 6, containersTaken: 1, pieces: 44, piecesTaken: 5, rewards: 10, given: 1, untracked: 6, maxHearts: 5, maxQuarters: 2,
	};
	const coopMode = S === "lobby-join" ? "continue" : "new";
	const coop = {
		setting: coopMode === "new" ? "new" : worlds[0].id, mode: coopMode, world: coopMode === "continue" ? worlds[0].id : "",
		dryRun: false, merge: false, liveSwitches: false, on: playing, myDryRun: false, autoYes: false,
		relays: { bossKill: "", refresh: "" },
		choice: { mode: coopMode, world: worlds[0].id, dryRun: false, merge: false, liveSwitches: false, iso: "" },
		cards: playing ? null : {
			card: "MemoryCardA.USA.raw", hint: "Load File 2 after Start: it binds to this world.", short: "",
			files: [
				{ slot: 1, state: "used", name: "Link", hearts: 9, bind: "this", bindText: "Bound to this world", world: worlds[0].id },
				{ slot: 2, state: "used", name: "Stephen", hearts: 4, bind: "unbound", bindText: "Not bound to any world" },
				{ slot: 3, state: "empty", name: "", hearts: 0, bind: "", bindText: "" },
			],
		},
		panel: playing ? {
			partners: ["Blake"],
			pipes: [{
				player: "Stephen", state: "bound", headline: "Bound and writing: your game follows world 7f3a91c2", tone: "ok",
				world: worlds[0].id, seq: 412, written: 1832, escapes: 0, failed: 0, paused: false, dryRun: false,
				progression: [
					"progression granted: story|inventory|keys|life|magic",
					"story bits held back until this game catches up: Tetra's ship departs (needs Outset done)",
					"small keys of table 3 (DRC): 1 key(s), 3 of 4 source(s) taken, 2 of 4 door(s) open",
					"small keys of table 4 (FW): not seen yet (it is measured on the file's first tick in game)",
					"max hearts: derived",
				],
				puzzle: "room puzzles: 14 dan, 6 zone switches shared", combat: "combat: 37 kills shared, 2 bosses", tuner: "",
				partner: "Dragon Roost Cavern, room 3",
				shares: ["image", "puzzle", "combat", "switches", "story", "inventory", "keys", "life", "magic"],
				held: ["Tetra's ship departs (needs Outset done)"],
				keys: [
					{ dungeon: "DRC", name: "Dragon Roost Cavern", on: true, seen: true, have: 1, taken: 3, sources: 4, open: 2, doors: 4, target: 1, pending: false },
					{ dungeon: "FW", name: "Forbidden Woods", on: true, seen: false, have: 0, taken: 0, sources: 0, open: 0, doors: 0, target: 0, pending: false },
					{ dungeon: "TotG", name: "Tower of the Gods", on: false, why: "no key table for it", seen: false, have: 0, taken: 0, sources: 0, open: 0, doors: 0, target: 0, pending: false },
				],
				life: true, lifeWhy: "", room: noRoom ? null : room,
			}],
			hub: S === "ingame-host" ? {
				world: "7f3a91c2", seq: 412, features: "image|puzzle|combat|switches|story|inventory|keys|life|magic", merge: false,
				peers: [{ name: "Blake", state: "bound", file: "bound", where: "DRC room 3" }], lines: ["hub puzzles: 14 dan, 6 zone", "hub combat: 37 kills"],
			} : null,
		} : null,
		backup: "",
	};
	if (coop.panel) {
		const pipe = coop.panel.pipes[0];
		if (shareMode === "none") Object.assign(pipe, { shares: [], held: [] });
		if (shareMode === "nostory") Object.assign(pipe, { shares: pipe.shares.filter((f) => f !== "story"), held: [] });
		if (noKeys) Object.assign(pipe, { keys: [], noKeyTable: true });
		coop.panel.rules = rulesOf(new Set(pipe.shares));
	}
	// The host's Edit: a change reaches the hub (its features) and, a tick
	// later, every pipeline's grant.
	function setShares(on) {
		const set = new Set(["image", ...on]);
		const pipe = coop.panel.pipes[0];
		pipe.shares = ["image", "puzzle", "combat", "live-switches", "switches", "story", "inventory", "keys", "life", "magic"].filter((f) => set.has(f));
		if (coop.panel.hub) coop.panel.hub.features = pipe.shares.join("|");
		const merge = coop.panel.rules.rows.find((r) => r.id === "merge").on;
		coop.panel.rules = rulesOf(set);
		coop.panel.rules.rows.find((r) => r.id === "merge").on = merge;
	}

	function state() {
		if (S.startsWith("home") || S.startsWith("setup") || S === "mods" || S === "settings") {
			return { session: 0, phase: "idle", running: false, role: "", name: "", addr: "", addrMasked: "", endReason: "", failed: false, lobby: null, players: [], playerCount: 0, hostAddrs: [], latencyMs: 0, dialog: null, banner: null, showIPs: false, dolphin: { state: "" }, map: null, color: "", startDolphinMyself: false, coop: null };
		}
		return {
			session: 1, phase: over() ? "ended" : phase, running: !over(), role: host ? "Host" : "Join", name: "Stephen",
			addr: "192.168.1.42", addrMasked: "•••.•••.•••.42", endReason: "The host closed the session.", failed: over(),
			lobby: {
				member: legacy ? 0 : 2, host, phase: lobbyPhase, legacy, members: legacy ? [] : members, coop: coopMode, link: { state: S === "reconnecting" ? "reconnecting" : "up", attempts: 0 },
				canStart: host && !playing, notReady: host && !playing ? ["Sam"] : [],
			},
			players: legacy ? [{ name: "Blake", color: "#F08C1E" }] : [], playerCount: legacy ? 2 : 3, hostAddrs: [], latencyMs: playing ? 24 : 0,
			dialog: S === "dialog" ? {
				key: "bind:1", kind: "bind", title: "Bind this file?", text: "File 2 “Stephen” isn't bound to a co-op world. Bind it to world 7f3a91c2? Its progress joins the world.",
				actions: [{ id: "answer-yes", label: "Bind", primary: true, arg: "q1" }, { id: "answer-no", label: "Not now", arg: "q1" }], cancel: "answer-no:q1", autoYes: true,
			} : null,
			banner: S === "reconnecting" ? { kind: "reconnecting", title: "Reconnecting to the host…", text: "Your game keeps running; the others come back once the link does.", attempts: 3 } : null,
			showIPs: false,
			dolphin: playing ? { state: "in_game", label: "In game", pid: 4120, launched: true, manual: false, pipelines: true } : { state: "" },
			map: playing ? { selfOk: true, color: "#2C5CE0", units: 1500, players: [{ name: host ? "Blake" : "Blake", color: "#F08C1E", dx: 120, dz: -60 }, { name: "Sam", color: "#3C9A2E", dx: -2600, dz: 4100 }] } : null,
			color: "#2C5CE0", startDolphinMyself: false, coop,
		};
	}

	const answers = {
		Info: () => info, State: () => { const st = state(); armEnd(); return st; }, Setup: () => setupView,
		PatchStatus: () => (S === "setup-game" ? patchDone : setupView.patch),
		DetectDolphin: () => dolphinView, UserDirFor: () => dolphinView.userDir,
		Mods: () => mods, UpdateStatus: () => update, CheckUpdate: () => update,
		LanSessions: () => lan, ShareAddrs: () => share, CoopWorlds: () => worlds,
		CoopStatusText: () => "co-op: Stephen: bound", SendChat: (t) => ({ kind: "chat", from: "Stephen", text: t }),
		CheckColor: (t) => ({ hex: t.toUpperCase(), name: "" }),
		SetCoopShares: (on) => { setShares(on); return null; },
		SetMerge: (on) => { coop.merge = on; if (coop.panel) coop.panel.rules.rows.find((r) => r.id === "merge").on = on; return null; },
	};
	const listeners = [];
	window.go = { gui: { Bridge: new Proxy({}, {
		get: (_, name) => async (...args) => {
			const f = answers[name];
			return f ? JSON.parse(JSON.stringify(f(...args))) : null;
		},
	}) } };
	window.runtime = { EventsOn: (_, fn) => { listeners.push(fn); return () => {}; } };

	// A few log and chat lines once the page is up (a session's feed).
	if (!S.startsWith("home") && !S.startsWith("setup") && S !== "mods" && S !== "settings") {
		setTimeout(() => {
			const lines = [
				{ kind: "log", level: "ok", text: "Connected to the host." },
				{ kind: "chat", from: "Blake", text: "ready when you are" },
				{ kind: "log", level: "info", text: "lobby: Sam joined" },
				{ kind: "log", level: "ok", sync: true, text: "co-op: set t03 DRC chest 4" },
				{ kind: "log", level: "warn", sync: true, text: "co-op: holding Tetra's ship departs back until this game catches up" },
				{ kind: "chat", from: "Stephen", text: "on my way to DRC" },
			];
			for (const l of lines) for (const fn of listeners) fn(Object.assign({ session: 1 }, l));
		}, 300);
	}
	// "ended": the session ends a moment after the page first reads it.
	let endArmed = false;
	const armEnd = () => {
		if (S !== "ended" || endArmed) return;
		endArmed = true;
		setTimeout(() => { isOver = true; for (const fn of listeners) fn({ kind: "ended", session: 1 }); }, 600);
	};
	// Setup scenarios open on their step.
	if (S === "setup-game" || S === "setup-dolphin") {
		window.addEventListener("load", () => setTimeout(() => {
			const b = document.querySelector('#steps [data-step="' + (S === "setup-game" ? "game" : "dolphin") + '"]');
			if (b) b.click();
		}, 400));
	}
	if (S === "setup-game") {
		setupView.patch = Object.assign({}, patchDone, { stage: "patch", running: true, percent: 64, seq: 5, coop: { state: "", text: "" } });
	}
	if (S === "mods") {
		window.addEventListener("load", () => setTimeout(() => { const b = document.getElementById("mods-btn") || document.querySelector('[data-nav="mods"]'); if (b) b.click(); }, 400));
	}
	if (S === "settings") {
		window.addEventListener("load", () => setTimeout(() => { const b = document.getElementById("nav-settings"); if (b) b.click(); }, 400));
	}
	if (S === "setup-game" || S === "setup-dolphin") {
		window.addEventListener("load", () => setTimeout(() => { const b = document.getElementById("setup-btn") || document.querySelector('[data-nav="setup"]'); if (b) b.click(); }, 200));
	}
	// &click=a,b: each id clicked in turn, after the scenarios' own clicks.
	const clicks = (q.get("click") || "").split(",").filter((c) => c);
	if (clicks.length) {
		window.addEventListener("load", () => clicks.forEach((id, i) => setTimeout(() => {
			const b = document.getElementById(id);
			if (b) b.click();
		}, 900 + 300 * i)));
	}
})();
