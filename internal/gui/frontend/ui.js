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
