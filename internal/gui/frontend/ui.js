// ui.js: the small DOM helpers the screens share (no Go calls here; those
// go through transport.js).

export const $ = (id) => document.getElementById(id);

export function el(tag, props = {}, ...kids) {
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

export function toast(text, { bad = false, actions = [] } = {}) {
	const t = el("div", { class: "toast" + (bad ? " bad" : "") }, el("div", {}, text));
	for (const [label, fn] of actions) {
		t.append(el("button", { class: "btn", type: "button", onclick: () => { t.remove(); fn(); } }, label));
	}
	$("toasts").append(t);
	setTimeout(() => t.remove(), actions.length ? 15000 : 7000);
}

// swatches renders palette into box as item slots. selected is a
// "#RRGGBB" or ""; a selected color outside the palette (a custom one)
// gets a slot of its own after it. Any color can be picked, whoever else
// wears it, and there is no "automatic" choice: a player who picked none
// wears classic green (owner decisions, 2026-10-16).
export function swatches(box, palette, selected, onPick, { disabled = false } = {}) {
	box.replaceChildren();
	for (const s of palette) {
		const label = s.name + (s.classic ? " (classic)" : "");
		box.append(el("button", {
			type: "button", class: "swatch", role: "radio", style: "--c:" + s.hex, title: label, "aria-label": label,
			"aria-checked": String((selected || "").toUpperCase() === s.hex), disabled,
			onclick: () => onPick(s.hex),
		}, el("i")));
	}
	const sel = (selected || "").toUpperCase();
	if (sel && !palette.some((s) => s.hex === sel)) {
		box.append(el("button", {
			type: "button", class: "swatch custom", role: "radio", style: "--c:" + sel, title: "Your own color " + sel,
			"aria-label": "your own color " + sel, "aria-checked": "true", disabled, onclick: () => onPick(sel),
		}, el("i")));
	}
}

// shade darkens (f < 1) or lightens (f > 1) a "#RRGGBB".
export function shade(hex, f) {
	const n = parseInt(hex.slice(1), 16);
	const ch = (v) => Math.max(0, Math.min(255, Math.round(f < 1 ? v * f : v + (255 - v) * (f - 1))));
	const r = ch((n >> 16) & 255), g = ch((n >> 8) & 255), b = ch(n & 255);
	return "#" + ((1 << 24) | (r << 16) | (g << 8) | b).toString(16).slice(1).toUpperCase();
}

// ---------- the dialog ----------
//
// One modal dialog at a time (the page's <dialog id="dlg">), as a leafy
// panel with a bark plaque. openDialog({title, text, detail, actions})
// shows it; each action is {label, primary, danger, run}, and the dialog
// closes before run unless keep is set. Escape closes it like the last
// action labelled Cancel / OK would, without running anything, except
// that onCancel runs then (a co-op question's no); a close made by code
// (closeDialog) never runs onCancel. extra is a node shown under the
// text (a list, option cards, a live check: coop.js).
//
// settle is for a dialog that asks something the player could answer by
// accident (a co-op question, "Remove the tag"): it pops up while they
// type in the chat. Focus goes to the dialog itself, not to a button, so
// a key already on its way presses nothing, and for SETTLE_MS after it
// opens every click, Enter, Space and Escape in it is ignored: input given
// before a question is shown never answers it (docs/08 Appendix C items
// 64 and 164; the TUI's questionSettle).

export const SETTLE_MS = 600;

let dlgOnClose = null;
let dlgOnCancel = null;
let dlgActed = false;
let dlgCancelled = false; // Escape closed it (the "cancel" event)
let dlgSettleUntil = 0;

function settling() {
	return performance.now() < dlgSettleUntil;
}

export function openDialog({ title, text, detail = "", actions = [], key = null, onClose = null, extra = null, onCancel = null, settle = false }) {
	const d = $("dlg");
	$("dlg-title").textContent = title;
	$("dlg-text").textContent = text;
	$("dlg-detail").textContent = detail;
	$("dlg-extra").replaceChildren(...(extra ? [extra] : []));
	const row = $("dlg-actions");
	row.replaceChildren(...actions.map((a) => el("button", {
		type: "button", class: "btn" + (a.primary ? " primary" : "") + (a.danger ? " danger" : ""),
		onclick: () => {
			if (!a.keep) {
				dlgActed = true;
				closeDialog();
			}
			if (a.run) a.run();
		},
	}, a.label)));
	d.dataset.key = key || "";
	dlgOnClose = onClose;
	dlgOnCancel = onCancel;
	dlgActed = false;
	dlgCancelled = false;
	dlgSettleUntil = settle ? performance.now() + SETTLE_MS : 0;
	if (!d.open) d.showModal();
	if (settle) {
		d.focus(); // no button: a key on its way presses nothing
		return;
	}
	const first = row.querySelector(".primary") || row.querySelector("button");
	if (first) first.focus();
}

// closeDialog closes the dialog (and calls its onClose once).
export function closeDialog() {
	const d = $("dlg");
	if (d.open) d.close();
}

// dialogKey is the key of the open dialog ("" for none or a local one).
export function dialogKey() {
	const d = $("dlg");
	return d.open ? d.dataset.key : "";
}

// setDialogText redraws the open dialog's text and detail (a live one's
// detail changes, and both are masked again when the switch goes off).
export function setDialogText(text, detail, extra) {
	$("dlg-text").textContent = text;
	$("dlg-detail").textContent = detail;
	if (extra !== undefined) $("dlg-extra").replaceChildren(...(extra ? [extra] : []));
}

export function initDialog() {
	const d = $("dlg");
	// A settling dialog takes no input yet (openDialog's settle).
	d.addEventListener("click", (e) => {
		if (settling() && e.target.closest("button")) {
			e.preventDefault();
			e.stopImmediatePropagation();
		}
	}, true);
	d.addEventListener("keydown", (e) => {
		if (settling() && (e.key === "Enter" || e.key === " " || e.key === "Escape")) {
			e.preventDefault();
			e.stopImmediatePropagation();
		}
	}, true);
	d.addEventListener("cancel", (e) => {
		if (settling()) {
			e.preventDefault();
			return;
		}
		dlgCancelled = true; // Escape: the close that follows is the player's no
	});
	d.addEventListener("close", () => {
		if ($("dlg").open) return; // closed and opened again before this event
		const f = dlgOnClose;
		const cancel = dlgActed || !dlgCancelled ? null : dlgOnCancel;
		dlgOnClose = dlgOnCancel = null;
		dlgActed = false;
		dlgCancelled = false;
		$("dlg").dataset.key = "";
		if (f) f();
		if (cancel) cancel();
	});
}
