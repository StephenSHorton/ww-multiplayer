// transport.js: the only module that talks to Go (design §4.2).
//
// In the app window, Wails binds the Go Bridge as window.go.gui.Bridge and
// delivers the controller's events as the "ww:event" event. Served by
// `ww-multiplayer ui --browser`, the same calls go to POST /api/call/<Method>
// (a JSON array of arguments; the answer is {result} or {error}) and the
// events arrive as server-sent events on /api/events. Either way a call
// returns a promise that rejects with an Error carrying Go's message.
//
// Moving to another shell (Wails v3, a plain web UI) changes this file only.

const token = new URLSearchParams(location.search).get("token") || "";

function bound() {
	return window.go && window.go.gui && window.go.gui.Bridge;
}

// mode is "window" inside the app, "browser" for ui --browser.
export function mode() {
	return bound() ? "window" : "browser";
}

// call runs the Bridge method name with args.
export async function call(name, ...args) {
	const b = bound();
	if (b) {
		try {
			return await b[name](...args);
		} catch (e) {
			throw new Error(typeof e === "string" ? e : (e && e.message) || String(e));
		}
	}
	const r = await fetch("/api/call/" + encodeURIComponent(name), {
		method: "POST",
		headers: { "Content-Type": "application/json", "X-WW-Token": token },
		body: JSON.stringify(args),
	});
	let body;
	try {
		body = await r.json();
	} catch {
		throw new Error("the app didn't answer (HTTP " + r.status + ")");
	}
	if (body.error) throw new Error(body.error);
	return body.result;
}

// onEvent calls fn with each controller event and returns a func that
// stops listening.
export function onEvent(fn) {
	if (bound() && window.runtime) {
		return window.runtime.EventsOn("ww:event", fn);
	}
	const es = new EventSource("/api/events?token=" + encodeURIComponent(token));
	es.onmessage = (m) => {
		let ev;
		try {
			ev = JSON.parse(m.data);
		} catch {
			return;
		}
		fn(ev);
	};
	return () => es.close();
}
