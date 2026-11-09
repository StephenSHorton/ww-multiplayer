// display.js: the Settings page's Display card, the widescreen setting
// (display.go): Off, 16:9, 16:10, 21:9 or a custom whole-number W:H.
// The app launches Dolphin with it at the next Start. Go validates and
// saves; this draws what it saved.

import { call } from "./transport.js";
import { $ } from "./ui.js";

const presets = ["off", "16:9", "16:10", "21:9"];
let saved = "off"; // what Go has
let custom = false; // Custom picked, its W:H not saved yet

function seg() {
	return $("ws-seg").children;
}

// render draws saved, or the open Custom fields.
function render() {
	const pick = custom || !presets.includes(saved) ? "custom" : saved;
	for (const b of seg()) b.setAttribute("aria-checked", String(b.dataset.ws === pick));
	$("ws-custom").hidden = pick !== "custom";
	if (pick === "custom" && !custom) {
		const [w, h] = saved.split(":");
		$("ws-w").value = w;
		$("ws-h").value = h;
	}
}

async function save(aspect) {
	$("ws-err").textContent = "";
	try {
		await call("SetWidescreen", aspect);
		custom = false;
	} catch (e) {
		$("ws-err").textContent = e.message;
	}
	await refreshDisplay();
}

// saveCustom saves the two fields once both hold a valid aspect.
function saveCustom() {
	const w = Number($("ws-w").value), h = Number($("ws-h").value);
	if (!$("ws-w").value || !$("ws-h").value) return;
	if (!Number.isInteger(w) || !Number.isInteger(h) || w < 1 || h < 1) {
		$("ws-err").textContent = "Width and height must be whole numbers above 0.";
		return;
	}
	if (w * 3 <= h * 4) {
		$("ws-err").textContent = w + ":" + h + " is not wider than 4:3: pick Off instead.";
		return;
	}
	if (w > h * 4) {
		$("ws-err").textContent = "Aspects wider than 4:1 aren't supported.";
		return;
	}
	save(w + ":" + h);
}

export function initDisplay() {
	for (const b of seg()) {
		b.addEventListener("click", () => {
			$("ws-err").textContent = "";
			if (b.dataset.ws !== "custom") {
				save(b.dataset.ws);
				return;
			}
			if (presets.includes(saved)) {
				custom = true;
				$("ws-w").value = "";
				$("ws-h").value = "";
			}
			render();
			$("ws-w").focus();
		});
	}
	$("ws-w").addEventListener("change", saveCustom);
	$("ws-h").addEventListener("change", saveCustom);
}

// refreshDisplay reads the saved setting (unset is Off) and draws it.
export async function refreshDisplay() {
	try {
		saved = (await call("Widescreen")) || "off";
	} catch {
		return;
	}
	render();
}
