// mods.js: the Mods page (docs/api/00-design.md §6.5). Each of the app's
// mods (Bridge.Mods) with its version, its status and the reason, a
// toggle that turns it on or off from the next time the app starts
// (Bridge.SetModEnabled, saved as config mods.<id>.enabled), a Patch
// button beside NEEDS_REPATCH (the Setup's Game step patches the disc
// again) and the mod's commands (Bridge.CallMod). The reasons come masked
// from Go unless the "Show IP addresses" switch is on, like every line.

import { call } from "./transport.js";
import { $, el, toast, openDialog } from "./ui.js";

let ctx = null; // { openSetup }
let seq = 0; // the newest Mods call: an older reply is dropped

const STATUS_WORDS = {
	LOADED: "Running",
	DISABLED: "Off",
	FAILED: "Failed",
	QUARANTINED: "Stopped",
	NEEDS_REPATCH: "Needs a re-patch",
};

export function initMods(c) {
	ctx = c;
}

// refreshMods reads the mods again and draws the page.
export async function refreshMods() {
	const my = ++seq;
	let v;
	try {
		v = await call("Mods");
	} catch (e) {
		toast(e.message, { bad: true });
		return;
	}
	if (my !== seq) return;
	render(v);
}

function render(v) {
	const list = $("mod-list");
	list.replaceChildren(...v.mods.map(row));
	if (!v.mods.length) list.append(el("li", { class: "empty" }, "This app runs no mods."));
	$("mods-note").textContent = v.dev
		? "Dev mods are listed because the app was started with --dev."
		: "";
}

function row(m) {
	const status = el("span", { class: "chip modstate " + m.status.toLowerCase(), title: m.statusText }, STATUS_WORDS[m.status] || m.status);
	const head = el("div", { class: "modhead" },
		el("span", { class: "modname" }, m.name || m.id),
		el("span", { class: "modver" }, m.version ? "v" + m.version : ""),
		m.dev ? el("span", { class: "chip dev", title: "A dev mod: shown with --dev" }, "Dev") : null,
		status,
		toggle(m));
	const lines = [];
	if (m.description) lines.push(el("p", { class: "moddesc" }, m.description));
	if (m.reason) lines.push(el("p", { class: "modreason" }, m.reason));
	if (m.requires.length) lines.push(el("p", { class: "modmeta" }, "Needs " + m.requires.join(", ")));
	if (m.pending) lines.push(el("p", { class: "modmeta pending" }, (m.enabled ? "On" : "Off") + " the next time you open the app."));
	if (m.flag) lines.push(el("p", { class: "modmeta" }, "Turned " + m.flag + " for this run by the app's command line."));
	const acts = [];
	if (m.status === "NEEDS_REPATCH") {
		acts.push(el("button", {
			type: "button", class: "btn small primary", "data-patch": m.id,
			title: "Patch your disc again: the patched ISO lacks " + (m.missing || []).join(", "),
			onclick: () => openDialog({
				title: "Patch your disc again?",
				text: "This mod requires re-patching your ISO; save states from the old patch will stop working.",
				actions: [{ label: "Patch", primary: true, run: () => ctx.openSetup("game") }, { label: "Cancel" }],
			}),
		}, "Patch"));
	}
	for (const verb of m.verbs) {
		acts.push(el("button", { type: "button", class: "btn small", "data-verb": verb, onclick: () => runVerb(m, verb) }, verb));
	}
	return el("li", { class: "mod", "data-mod": m.id }, head, ...lines,
		acts.length ? el("div", { class: "modacts" }, ...acts) : null);
}

// toggle is the mod's on/off switch, a slot like the header's switch.
function toggle(m) {
	return el("button", {
		type: "button", class: "switch modtoggle", role: "switch", "aria-checked": String(m.enabled),
		"aria-label": (m.enabled ? "Turn off " : "Turn on ") + (m.name || m.id),
		title: m.enabled ? "On from the next start: click to turn it off" : "Off from the next start: click to turn it on",
		onclick: () => setEnabled(m, !m.enabled),
	}, el("i", { class: "knob", "aria-hidden": "true" }), m.enabled ? "On" : "Off");
}

async function setEnabled(m, on) {
	const go = async () => {
		try {
			await call("SetModEnabled", m.id, on);
		} catch (e) {
			toast(e.message, { bad: true });
		}
		refreshMods(); // the "mods" event redraws too
	};
	if (!on && m.net) {
		openDialog({
			title: "Turn off " + (m.name || m.id) + "?",
			text: "Without it there is no Host or Join, and the mods that need it don't run, until you turn it back on.",
			actions: [{ label: "Turn off", primary: true, run: go }, { label: "Cancel" }],
		});
		return;
	}
	await go();
}

async function runVerb(m, verb) {
	try {
		const r = await call("CallMod", m.id, verb, null);
		toast((m.name || m.id) + ": " + verb + (r.ok ? " done" : " ran" + (r.code ? " (" + r.code + ")" : "")));
	} catch (e) {
		toast(e.message, { bad: true });
	}
}
