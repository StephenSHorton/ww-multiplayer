// bugreport.js: the header's "Bug report" button. Bridge.SaveBugReport
// writes one zip with the session logs, Dolphin's log and the
// diagnostics, every file redacted and no game files or saves
// (internal/bugreport); this says where it is and, in the window, opens
// its folder. Nothing is uploaded. Self-contained (its own <script> in
// index.html), so the rest of the frontend doesn't know it is there.

import { call, mode } from "./transport.js";
import { $, toast } from "./ui.js";

const btn = $("bugreport-btn");
if (btn) {
	btn.addEventListener("click", async () => {
		btn.disabled = true;
		try {
			const r = await call("SaveBugReport");
			const left = r.skipped && r.skipped.length ? " (" + r.skipped.length + " file(s) left out: see contents.txt)" : "";
			const where = "Bug report saved: " + r.path + left + ". Attach it to your issue; nothing was sent.";
			if (mode() === "window") {
				toast(where, { actions: [["Show folder", () => call("OpenFolder", "bugreports").catch((e) => toast(e.message, { bad: true }))]] });
			} else {
				toast(where);
			}
		} catch (e) {
			toast("Couldn't save the bug report: " + e.message, { bad: true });
		} finally {
			btn.disabled = false;
		}
	});
}
