// theme.js: the Settings page's theme (System, Light or Dark). The
// choice is this computer's only, so it lives in the page's localStorage
// ("ww-theme": "light" or "dark"; none = System, which follows the
// system's prefers-color-scheme). index.html's head script sets
// html[data-theme] from it before the first paint; app.css has the
// tokens of both. Storage may be unavailable (a private window, blocked
// site data): the theme then follows the system and the choice lasts
// until the window closes.

import { $ } from "./ui.js";

const KEY = "ww-theme";

function saved() {
	try {
		const t = localStorage.getItem(KEY);
		return t === "light" || t === "dark" ? t : "system";
	} catch {
		return "system";
	}
}

function apply(t) {
	if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
	else delete document.documentElement.dataset.theme;
	for (const b of $("theme-seg").children) b.setAttribute("aria-checked", String(b.dataset.theme === t));
}

export function initTheme() {
	apply(saved());
	for (const b of $("theme-seg").children) {
		b.addEventListener("click", () => {
			const t = b.dataset.theme;
			try {
				if (t === "system") localStorage.removeItem(KEY);
				else localStorage.setItem(KEY, t);
			} catch {
				// not kept: this window only
			}
			apply(t);
		});
	}
}
