// setup.js: the first-run Setup screen (App PR 6, design §2.1): your name
// and tunic color, your game (patch the disc, or take a patched one), and
// Dolphin (detected, or browsed for; on macOS the Prepare Dolphin step),
// then "You're set". Each step can be skipped; Home's Setup button opens
// it again. Everything reaches Go through transport.js.

import { call, mode } from "./transport.js";
import { $, el, toast, swatches, shade } from "./ui.js";

const STEPS = ["profile", "game", "dolphin", "done"];
const SOURCES = {
	running: "running now", DOLPHIN_EXE: "DOLPHIN_EXE", installed: "installed", registry: "installed",
	portable: "on your Desktop", scoop: "scoop", prepared: "prepared copy", PATH: "on PATH", default: "not found",
};

let ctx = null; // {info, show, onSaved}
let setup = null; // Bridge.Setup
let step = "profile";
let color = ""; // the profile step's color ("#RRGGBB")
let dolphin = null; // Bridge.DetectDolphin
let chosenExe = "";
// userDirEdited: the player typed or browsed the user folder, so choosing
// another Dolphin keeps it; otherwise it follows the chosen Dolphin.
let userDirEdited = false;
let userDirAsk = 0; // the latest UserDirFor call; older replies are dropped
let patch = null; // the last PatchView

// initSetup wires the screen once and returns the Setup view the app
// decides on (does it open on the setup?).
export async function initSetup(c) {
	ctx = c;
	wire();
	setup = await call("Setup");
	if (setup.patch && setup.patch.running) onPatch(setup.patch);
	// Every launch: a macOS copy older than the Dolphin it came from.
	if (setup.mac && setup.mac.refresh && !setup.needed) {
		toast("Dolphin was updated — refresh the copy the app plays on.", { actions: [["Refresh the copy", () => openSetup("dolphin")]] });
	}
	return setup;
}

// openSetup shows the Setup screen at step at, or at the first step that
// needs something (the first one when nothing does).
export async function openSetup(at) {
	setup = await call("Setup");
	const first = setup.gaps.length ? setup.gaps[0].step : "profile";
	fillProfile();
	fillGame();
	ctx.show("setup");
	go(at || first);
}

function go(s) {
	step = s;
	for (const p of document.querySelectorAll("#setup .step")) p.hidden = p.dataset.step !== s;
	const i = STEPS.indexOf(s);
	for (const b of document.querySelectorAll("#steps button")) {
		const k = b.dataset.step;
		b.setAttribute("aria-current", String(k === s));
		b.classList.toggle("done", stepDone(k));
	}
	$("step-back").hidden = i === 0;
	$("step-skip").hidden = s === "done";
	$("setup-title").textContent = s === "done" ? (setup.needed ? "Almost there" : "You're set!") : setup.done ? "Setup" : "Welcome aboard";
	if (s === "dolphin") detect();
	if (s === "done") fillDone();
}

function next() {
	go(STEPS[Math.min(STEPS.indexOf(step) + 1, STEPS.length - 1)]);
}

function stepDone(k) {
	if (!setup) return false;
	if (k === "done") return !setup.needed;
	return !setup.gaps.some((g) => g.step === k);
}

async function refresh() {
	setup = await call("Setup");
	await ctx.onSaved();
	for (const b of document.querySelectorAll("#steps button")) b.classList.toggle("done", stepDone(b.dataset.step));
}

// ---------- step 1: name and color ----------

function fillProfile() {
	$("s-name").maxLength = ctx.info.maxName;
	$("s-name").value = setup.name || $("s-name").value || "";
	color = setup.color || ctx.info.palette[0].hex; // never chosen: classic green
	$("s-hex").value = color && !ctx.info.palette.some((p) => p.hex === color) ? color : "";
	$("s-color-note").textContent = "";
	renderColor();
}

function renderColor() {
	swatches($("s-swatches"), ctx.info.palette, color, (hex) => {
		color = hex;
		$("s-hex").value = "";
		$("s-color-note").textContent = "";
		renderColor();
	});
	const tunic = color;
	const fig = $("s-link");
	fig.style.setProperty("--tunic", tunic);
	fig.style.setProperty("--tunic-dk", shade(tunic, 0.62));
	fig.style.setProperty("--tunic-hi", shade(tunic, 1.25));
	$("s-well").value = tunic.toLowerCase();
	$("s-preview-name").textContent = $("s-name").value.trim() || "Link";
	const sw = ctx.info.palette.find((p) => p.hex === color);
	$("s-preview-color").textContent = sw ? cap(sw.name) + (sw.classic ? " (classic)" : "") : "Your own color " + color;
}

// customColor reads the custom field. Players may share a color (owner
// decision, 2026-10-16), so it only has to be a color.
async function customColor(text) {
	const note = $("s-color-note");
	note.className = "hint";
	if (!text.trim()) {
		note.textContent = "";
		return;
	}
	try {
		const c = await call("CheckColor", text);
		color = c.hex;
		note.textContent = c.name ? "That's " + c.name + " from the palette." : "";
		renderColor();
	} catch (e) {
		note.className = "hint warn";
		note.textContent = e.message;
	}
}

async function saveProfile() {
	$("s-profile-error").textContent = "";
	try {
		await call("SaveProfile", { name: $("s-name").value, color });
		await refresh();
		next();
	} catch (e) {
		$("s-profile-error").textContent = e.message;
	}
}

// ---------- step 2: the game ----------

function fillGame() {
	const browser = !setup.fileDialog;
	$("s-iso-typed").hidden = !browser;
	$("s-iso-pick").textContent = browser ? "Patch this disc" : "Choose your disc…";
	const cur = $("s-game-current");
	cur.replaceChildren();
	const shown = patch && patch.stage !== "idle";
	if (setup.patched && !shown) {
		cur.append(check(setup.patchedOK ? "ok" : "fail", setup.patchedOK ? "Your patched disc" : "Your patched disc is gone", setup.patched));
	}
	$("s-game-error").textContent = "";
	if (patch) renderPatch();
}

function check(state, label, detail) {
	const mark = { ok: "✓", warn: "!", fail: "✕", pending: "…", wait: "" }[state] ?? "";
	return el("li", { class: "check " + state },
		el("span", { class: "mark", "aria-hidden": "true" }, mark),
		el("span", { class: "what" }, el("b", {}, label), detail ? el("small", {}, detail) : null));
}

async function startPatch(patched) {
	$("s-game-error").textContent = "";
	let path = "";
	if (setup.fileDialog) {
		try {
			path = await call("PickFile", "iso");
		} catch (e) {
			$("s-game-error").textContent = e.message;
			return;
		}
		if (!path) return; // cancelled
	} else {
		path = $("s-iso-path").value;
	}
	try {
		await call("StartPatch", { path, patched });
		onPatch(await call("PatchStatus"));
	} catch (e) {
		$("s-game-error").textContent = e.message;
	}
}

// onPatch shows a patch's progress (a "patch" event, or PatchStatus).
// Events and PatchStatus replies can arrive out of order, so a view no
// newer than the one shown (same patch, lower or equal seq) is dropped.
export function onPatch(p) {
	if (!p || (patch && (p.id < patch.id || (p.id === patch.id && p.seq <= patch.seq)))) return;
	const finished = patch && patch.running && !p.running && p.id === patch.id;
	patch = p;
	renderPatch();
	if (finished) refresh().then(fillGame);
}

function renderPatch() {
	const p = patch;
	const box = $("s-progress");
	box.hidden = p.stage === "idle";
	if (box.hidden) return;
	const pct = Math.max(0, Math.min(100, p.percent));
	$("s-meter").style.width = pct + "%";
	$("s-meter").classList.toggle("busy", p.running);
	$("s-pct").textContent = p.running ? pct + "%" : p.stage === "done" ? "Done" : p.stage === "canceled" ? "Stopped" : "Failed";
	const rows = $("s-checks");
	rows.replaceChildren();
	const writing = p.stage === "start" || p.stage === "patch";
	if (p.alreadyPatched) {
		rows.append(check("ok", "Already patched: used as it is", p.in));
	} else if (p.stage === "failed" && !p.coop.state) {
		rows.append(check("fail", "Patching the disc failed", p.error));
	} else if (p.stage === "canceled") {
		rows.append(stoppedRow(p));
	} else if (p.stage === "canceling") {
		rows.append(check("pending", "Stopping… (a disc already being written is finished first)", null));
	} else {
		rows.append(check(writing ? "pending" : "ok", writing ? "Patching your disc…" : "Disc patched", writing ? p.in : p.out));
	}
	if (p.coop.state === "pending") rows.append(check("pending", "Building the co-op tables…", null));
	else if (p.coop.state === "ok") rows.append(check("ok", "Co-op tables built", p.coop.text));
	else if (p.coop.state === "warn") rows.append(check("warn", "Co-op tables: a warning", p.coop.text));
	if (p.stage === "done") rows.append(check("ok", "Saved: the app boots this disc", p.image));
	if (p.stage === "failed" && p.coop.state) rows.append(check("fail", "Something went wrong", p.error));
	$("s-patch-log").replaceChildren(...p.lines.map((l) => el("li", {}, l)));
	$("s-patch-cancel").hidden = !p.running;
	for (const id of ["s-iso-pick", "s-iso-have"]) $(id).disabled = p.running;
}

// stoppedRow says what a cancelled patch left (PatchView.stopped).
function stoppedRow(p) {
	switch (p.stopped) {
	case "removed":
		return check("warn", "Stopped: the new disc was finished, then removed", null);
	case "replaced":
		return check("warn", "Stopped, but the new disc was already written over your earlier one, so it was kept", p.out);
	case "kept":
		return check("warn", "Stopped, but the new disc couldn't be removed", p.out);
	}
	return check("warn", "Stopped: nothing was changed", null);
}

// ---------- step 3: Dolphin ----------

async function detect() {
	$("s-dolphin-error").textContent = "";
	dolphin = await call("DetectDolphin");
	chosenExe = dolphin.savedOK ? dolphin.saved : dolphin.found ? dolphin.exe : "";
	userDirEdited = false;
	userDirAsk++;
	$("s-userdir").value = dolphin.savedOK && dolphin.savedUserDir ? dolphin.savedUserDir : dolphin.found ? dolphin.userDir : "";
	$("s-dolphin-path").hidden = setup.fileDialog;
	$("s-dolphin-browse").textContent = setup.fileDialog ? "Browse…" : "Use this path";
	$("s-userdir-browse").hidden = !setup.fileDialog;
	renderDolphin();
	renderMac(dolphin.mac);
}

function renderDolphin() {
	const list = $("s-cands");
	list.replaceChildren();
	const rows = dolphin.candidates.filter((c) => c.found);
	if (dolphin.saved && !rows.some((c) => same(c.exe, dolphin.saved))) {
		rows.unshift({ exe: dolphin.saved, source: "saved", found: dolphin.savedOK });
	}
	if (chosenExe && !rows.some((c) => same(c.exe, chosenExe))) rows.unshift({ exe: chosenExe, source: "chosen", found: true });
	for (const c of rows) {
		const on = same(c.exe, chosenExe);
		list.append(el("li", {},
			el("button", {
				type: "button", class: "cand", role: "radio", "aria-checked": String(on), disabled: !c.found,
				onclick: () => choose(c.exe),
			}, el("span", { class: "path" }, el("bdi", { dir: "ltr" }, c.exe)),
			el("span", { class: "chip" + (c.source === "running" ? " ready" : "") }, c.found ? SOURCES[c.source] || c.source : "missing"))));
	}
	if (!rows.length) {
		list.append(el("li", { class: "empty" }, "No Dolphin found. Looked in: " + dolphin.candidates.map((c) => c.exe).join(", ")));
	}
	$("s-dolphin-save").disabled = !chosenExe;
}

// choose makes exe the chosen Dolphin. Unless the player set the user
// folder themselves, the box follows: the folder this Dolphin uses (its
// User folder when portable), as SetDolphin would work it out.
async function choose(exe) {
	const changed = !same(exe, chosenExe);
	chosenExe = exe;
	renderDolphin();
	if (!changed || userDirEdited) return;
	const ask = ++userDirAsk;
	$("s-userdir").value = "";
	try {
		const dir = await call("UserDirFor", exe);
		if (ask === userDirAsk && !userDirEdited) $("s-userdir").value = dir;
	} catch {
		// left empty: SetDolphin works it out when saving
	}
}

function cap(s) {
	return s.charAt(0).toUpperCase() + s.slice(1);
}

function same(a, b) {
	return a.toLowerCase() === b.toLowerCase();
}

function renderMac(m) {
	const box = $("s-mac");
	box.hidden = !m || m.state === "ready";
	if (box.hidden) return;
	$("s-mac-text").textContent = m.text;
	const b = $("s-mac-prepare");
	b.hidden = !m.prepare;
	b.textContent = m.refresh ? "Refresh the copy" : "Prepare Dolphin";
}

async function browseDolphin() {
	$("s-dolphin-error").textContent = "";
	try {
		const p = await call("PickFile", "dolphin");
		if (p) await choose(p);
	} catch (e) {
		$("s-dolphin-error").textContent = e.message;
	}
}

async function saveDolphin() {
	$("s-dolphin-error").textContent = "";
	try {
		await call("SetDolphin", { exe: chosenExe, userDir: $("s-userdir").value });
		await refresh();
		next();
	} catch (e) {
		$("s-dolphin-error").textContent = e.message;
	}
}

async function prepare() {
	const b = $("s-mac-prepare");
	b.disabled = true;
	$("s-mac-log").hidden = false;
	$("s-mac-log").replaceChildren(el("li", {}, "Preparing… (copying Dolphin can take a minute)"));
	try {
		const r = await call("PrepareDolphin");
		$("s-mac-log").replaceChildren(...r.lines.map((l) => el("li", {}, l)), r.error ? el("li", { class: "err" }, r.error) : null);
		if (r.ok) {
			toast("Dolphin is ready: the app plays on the prepared copy.");
			await refresh();
			await detect();
		}
	} catch (e) {
		$("s-mac-log").append(el("li", { class: "err" }, e.message));
	} finally {
		b.disabled = false;
	}
}

// ---------- the end ----------

function fillDone() {
	const rows = $("s-summary");
	rows.replaceChildren();
	const sw = ctx.info.palette.find((p) => p.hex === setup.color);
	rows.append(setup.name
		? check("ok", "You: " + setup.name, setup.color ? (sw ? cap(sw.name) : setup.color) + " tunic" : "Classic green tunic")
		: check("warn", "No name yet", "Pick one in step 1, or on Home."));
	rows.append(setup.patchedOK ? check("ok", "Your patched disc", setup.patched) : check("warn", "No patched disc yet", "Step 2 patches your disc."));
	rows.append(setup.dolphinOK ? check("ok", "Dolphin", setup.dolphinExe) : check("warn", "No Dolphin yet", "Step 3 finds it."));
	$("s-done-plaque").textContent = setup.needed ? "Almost there" : "You're set";
	$("s-done-text").textContent = setup.needed
		? "Almost there: what's missing can wait. Setup opens again next time until it's done, or use the Setup button."
		: "You're set. Host or join a session.";
}

async function finish() {
	try {
		await call("FinishSetup");
	} catch (e) {
		toast(e.message, { bad: true });
	}
	await ctx.onSaved();
	ctx.show("home");
}

// ---------- wiring ----------

function wire() {
	for (const b of document.querySelectorAll("#steps button")) b.addEventListener("click", () => go(b.dataset.step));
	$("step-back").addEventListener("click", () => go(STEPS[Math.max(STEPS.indexOf(step) - 1, 0)]));
	$("step-skip").addEventListener("click", next);
	$("s-name").addEventListener("input", renderColor);
	$("s-hex").addEventListener("change", () => customColor($("s-hex").value));
	$("s-hex").addEventListener("input", () => {
		if (/^#?[0-9a-f]{6}$/i.test($("s-hex").value.trim())) customColor($("s-hex").value);
	});
	$("s-well").addEventListener("change", () => {
		$("s-hex").value = $("s-well").value.toUpperCase();
		customColor($("s-hex").value);
	});
	$("s-profile-form").addEventListener("submit", (e) => {
		e.preventDefault();
		saveProfile();
	});
	$("s-iso-pick").addEventListener("click", () => startPatch(false));
	$("s-iso-have").addEventListener("click", () => startPatch(true));
	$("s-patch-cancel").addEventListener("click", () => call("CancelPatch"));
	$("s-game-next").addEventListener("click", next);
	$("s-dolphin-browse").addEventListener("click", () => {
		if (setup.fileDialog) browseDolphin();
		else if ($("s-dolphin-path").value.trim()) choose($("s-dolphin-path").value.trim());
	});
	$("s-userdir-browse").addEventListener("click", async () => {
		try {
			const p = await call("PickFile", "userdir");
			if (p) {
				userDirEdited = true;
				$("s-userdir").value = p;
			}
		} catch (e) {
			$("s-dolphin-error").textContent = e.message;
		}
	});
	$("s-userdir").addEventListener("input", () => {
		userDirEdited = true;
	});
	$("s-dolphin-rescan").addEventListener("click", detect);
	$("s-dolphin-save").addEventListener("click", saveDolphin);
	$("s-mac-prepare").addEventListener("click", prepare);
	$("s-finish").addEventListener("click", finish);
	if (mode() === "browser") $("s-iso-path").placeholder = "C:\\Games\\Wind Waker.ciso";
}
