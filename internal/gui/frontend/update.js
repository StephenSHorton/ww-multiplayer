// update.js: one-click updates (owner decision 2026-10-17). At start the
// window asks Go to check GitHub's latest release (CheckUpdate: at most
// every few hours, never in a dev build, not when "Check for updates" is
// off); a newer one shows the banner "vX.Y.Z is out" with Update, Release
// notes and Later. Update downloads it, checks it against SHA256SUMS and
// swaps it in (Go: internal/update); the app then closes and opens again
// on the new version. The banner shows on Home, the Mods page and
// Settings only: never during a session (it is back after Leave) or the
// Setup. A session
// or patch that started meanwhile leaves the update "ready": the app
// restarts after Leave (or Restart). Every start re-patches a disc that
// another version patched, the new version's first start above all
// (afterUpdate). The Settings page's Updates card (renderSettings; the
// sidebar's version pill opens it) has the version, the "Check for
// updates" switch, Check now and the release notes.

import { call, mode } from "./transport.js";
import { $, toast } from "./ui.js";

let ctx = null; // {view, running, openSetup, onPatch}
let upd = null; // Bridge.UpdateStatus
let dismissed = ""; // the version Later hid, this run
let checking = false; // Settings' Check now is waiting for its reply

export function initUpdate(c) {
	ctx = c;
	$("upd-go").addEventListener("click", install);
	$("upd-notes").addEventListener("click", notes);
	$("upd-later").addEventListener("click", () => {
		dismissed = upd ? upd.latest : "";
		render();
	});
	$("upd-cancel").addEventListener("click", () => call("CancelUpdate").catch(() => {}));
	$("upd-restart").addEventListener("click", async () => {
		$("upd-error").textContent = "";
		try {
			await call("RestartUpdate");
		} catch (e) {
			$("upd-error").textContent = e.message;
		}
	});
	$("set-notes").addEventListener("click", notes);
	$("set-checknow").addEventListener("click", checkNow);
	$("set-checkupd").addEventListener("change", async () => {
		const box = $("set-checkupd");
		try {
			await call("SetCheckUpdates", box.checked);
		} catch (e) {
			toast(e.message, { bad: true });
			box.checked = !box.checked;
		}
	});
}

// startUpdate runs once the first screen shows: finish an update this
// start follows, or a re-patch an earlier start didn't finish, then check
// for a newer one.
export async function startUpdate() {
	try {
		onUpdate(await call("UpdateStatus"));
	} catch {
		return;
	}
	if (upd.installed || upd.repatch === "repatch" || upd.repatch === "pick") await afterUpdate(upd);
	try {
		onUpdate(await call("CheckUpdate", false));
	} catch {
		// No network: no banner, nothing to say.
	}
}

// afterUpdate is the first start of a version an update installed, or a
// start whose disc another version patched (an interrupted re-patch).
async function afterUpdate(u) {
	const why = u.installed
		? "Updated to " + u.installed + ". "
		: "Your patched disc was made by another version of this app. ";
	switch (u.repatch) {
	case "repatch":
		toast(why + "Patching your disc again with this version…");
		await ctx.openSetup("game");
		try {
			await call("Repatch");
			ctx.onPatch(await call("PatchStatus"));
		} catch (e) {
			toast(e.message, { bad: true });
		}
		break;
	case "pick":
		toast(why + "Choose your vanilla Wind Waker disc so the app can patch it with this version.", { bad: true });
		await ctx.openSetup("game");
		break;
	default:
		toast(why.trim());
	}
}

// onUpdate takes a newer view (an "update" event or a reply).
export function onUpdate(u) {
	if (!u || (upd && u.seq < upd.seq)) return;
	upd = u;
	render();
	if (ctx && ctx.view() === "settings") renderSettings(false);
}

// render draws the banner for the screen showing now (and Settings'
// version line, which points at the banner only while there is one).
export function render() {
	if (upd && ctx && ctx.view() === "settings" && !checking) $("set-vertext").textContent = versionText(upd);
	const bar = $("updbar");
	const u = upd;
	const v = ctx ? ctx.view() : "home";
	const inSession = !!(ctx && ctx.running && ctx.running());
	const busy = u && (u.stage === "downloading" || u.stage === "installing" || u.stage === "restarting");
	const ready = u && u.stage === "ready";
	const show = !!u && (v === "home" || v === "mods" || v === "settings") && !inSession &&
		(busy || ready || u.stage === "done" || ((u.stage === "failed" || u.available) && dismissed !== u.latest));
	bar.hidden = !show;
	if (!show) return;
	const title = $("upd-title");
	const text = $("upd-text");
	$("upd-progress").hidden = !(u.stage === "downloading" || u.stage === "installing");
	$("upd-btns").hidden = busy || u.stage === "done";
	$("upd-go").hidden = !u.canInstall || ready;
	$("upd-later").hidden = ready;
	$("upd-restart").hidden = !ready;
	$("upd-go").textContent = u.stage === "failed" ? "Try again" : "Update";
	$("upd-error").textContent = u.stage === "failed" ? u.error : "";
	switch (u.stage) {
	case "downloading":
	case "installing":
		title.textContent = "Downloading " + u.latest + "…";
		text.textContent = "Checking it against the release's checksum before anything is replaced.";
		$("upd-meter").style.width = Math.max(0, Math.min(100, u.percent)) + "%";
		$("upd-meter").classList.toggle("busy", true);
		$("upd-pct").textContent = u.percent + "%";
		break;
	case "restarting":
		title.textContent = u.latest + " is installed";
		text.textContent = "The app is restarting on the new version…";
		break;
	case "done":
		title.textContent = u.latest + " is installed";
		text.textContent = "Close the app and open it again to use it.";
		break;
	case "ready":
		title.textContent = u.latest + " is installed";
		text.textContent = "The app restarts on it after you Leave, or now with Restart.";
		break;
	default:
		title.textContent = u.latest + " is out";
		text.textContent = u.canInstall
			? "You have " + u.current + ". Update downloads it, then the app restarts on it."
			: "You have " + u.current + ". Download it from the releases page.";
	}
}

async function install() {
	$("upd-error").textContent = "";
	try {
		await call("InstallUpdate");
		onUpdate(await call("UpdateStatus"));
	} catch (e) {
		$("upd-error").textContent = e.message;
	}
}

async function notes() {
	const u = upd || {};
	if (mode() === "window") {
		try {
			await call("OpenReleaseNotes");
		} catch (e) {
			toast(e.message, { bad: true });
		}
		return;
	}
	window.open(u.notesURL || "https://github.com/StephenSHorton/ww-multiplayer/releases", "_blank", "noopener");
}

// renderSettings fills the Settings page's Updates card from the newest
// UpdateStatus (read again when the page opens).
export async function renderSettings(fresh = true) {
	if (fresh) {
		try {
			upd = await call("UpdateStatus");
		} catch {
			return;
		}
	}
	const u = upd;
	if (!u) return;
	$("set-vertitle").textContent = "Version " + u.current;
	$("set-vertext").textContent = versionText(u);
	const box = $("set-checkupd");
	box.checked = u.enabled;
	box.disabled = u.dev;
	$("set-checknow").disabled = u.dev;
}

// versionText is Settings' version line. The banner never shows in a
// session, so there it says where Update will be.
function versionText(u) {
	if (u.dev) return "This is a development build (" + u.current + "): it doesn't check for updates.";
	if (u.available) {
		const where = ctx && ctx.running && ctx.running()
			? "Update is on the banner once you leave the session."
			: "Update is on the banner above.";
		return "You have " + u.current + ". " + u.latest + " is out: " + where;
	}
	return "You have " + u.current + (u.latest && u.latest === u.current ? ", the newest version." : ".");
}

async function checkNow() {
	$("set-upderr").textContent = "";
	$("set-vertext").textContent = "Checking…";
	$("set-checknow").disabled = true;
	checking = true;
	try {
		const u = await call("CheckUpdate", true);
		dismissed = "";
		onUpdate(u);
		$("set-vertext").textContent = u.dev || u.available ? versionText(u) : "You have " + u.current + ", the newest version.";
	} catch (e) {
		if (upd) $("set-vertext").textContent = versionText(upd);
		$("set-upderr").textContent = e.message;
	} finally {
		checking = false;
		$("set-checknow").disabled = !!(upd && upd.dev);
	}
}
