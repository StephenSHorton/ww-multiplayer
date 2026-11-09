// disc.js: the disc update's line on Play (card #1172, Go: disc.go). The
// app checks the patched disc when it opens, at Host and Join and before
// Dolphin launches; one another version patched is patched again by
// itself, and this line says so ("Updating your disc for v0.6.2… 15 s"),
// Setup never opens. A Dolphin holding the disc gets "Close Dolphin to
// update your disc" (the update starts by itself once it closes); no
// vanilla disc gets "Choose your Wind Waker disc" with Choose; a failed
// update gets Try again. It shows on Home and the Session screen.

import { call, mode } from "./transport.js";
import { $, toast } from "./ui.js";

let ctx = null; // {view, openSetup}
let disc = null; // Bridge.DiscStatus
let ticker = 0; // the seconds counter while patching

export function initDisc(c) {
	ctx = c;
	$("disc-retry").addEventListener("click", async () => {
		$("disc-error").textContent = "";
		try {
			await call("Repatch");
		} catch (e) {
			$("disc-error").textContent = e.message;
		}
	});
	$("disc-pick").addEventListener("click", pick);
}

// startDisc is the check when the window opens (Bridge.CheckDisc): a
// stale disc starts updating at once.
export async function startDisc() {
	try {
		onDisc(await call("CheckDisc"));
	} catch {
		// The line stays as the last event left it.
	}
}

// onDisc takes a newer line (a "disc" event or a reply).
export function onDisc(d) {
	if (!d || (disc && d.seq < disc.seq)) return;
	disc = d;
	renderDisc();
}

// renderDisc draws the line for the screen showing now.
export function renderDisc() {
	const bar = $("discbar");
	const d = disc;
	const v = ctx ? ctx.view() : "home";
	const show = !!d && !!d.state && (v === "home" || v === "session");
	bar.hidden = !show;
	clearInterval(ticker);
	ticker = 0;
	if (!show) return;
	const busy = d.state === "patching" || d.state === "closing";
	bar.classList.toggle("warn", d.state === "blocked" || d.state === "pick" || d.state === "failed");
	$("disc-spin").hidden = !busy;
	$("disc-title").textContent = d.title;
	$("disc-text").textContent = d.text;
	$("disc-pick").hidden = d.state !== "pick";
	$("disc-retry").hidden = d.state !== "failed";
	$("disc-error").textContent = d.state === "failed" ? d.error || "" : "";
	$("disc-progress").hidden = d.state !== "patching";
	if (d.state === "patching") {
		const pct = Math.max(0, Math.min(100, d.percent || 0));
		$("disc-meter").style.width = pct + "%";
		$("disc-pct").textContent = pct + "%";
		const tick = () => {
			const s = d.started ? Math.max(0, Math.round((Date.now() - Date.parse(d.started)) / 1000)) : 0;
			$("disc-title").textContent = d.title + " " + s + " s";
		};
		tick();
		ticker = setInterval(tick, 1000);
	}
}

// pick is the "pick" line's Choose: the disc dialog, then the patch (the
// window); a browser page has no file dialog, so Setup's game step opens.
async function pick() {
	$("disc-error").textContent = "";
	if (mode() !== "window") {
		ctx.openSetup("game");
		return;
	}
	let path = "";
	try {
		path = await call("PickFile", "iso");
	} catch (e) {
		ctx.openSetup("game");
		return;
	}
	if (!path) return;
	try {
		await call("StartPatch", { path });
	} catch (e) {
		toast(e.message, { bad: true });
	}
}
