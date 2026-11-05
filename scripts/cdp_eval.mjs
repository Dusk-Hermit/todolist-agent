// Dev-only end-to-end probe over the WebView2 DevTools protocol.
// Launch the app with WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9333
import http from "node:http";

const PORT = process.env.CDP_PORT || "9333";

function getJSON(path) {
  return new Promise((resolve, reject) => {
    http.get({ host: "127.0.0.1", port: Number(PORT), path }, (res) => {
      let body = "";
      res.on("data", (d) => (body += d));
      res.on("end", () => {
        try { resolve(JSON.parse(body)); } catch (e) { reject(new Error(body.slice(0, 200))); }
      });
    }).on("error", reject);
  });
}

const targets = await getJSON("/json");
const page = targets.find((t) => t.type === "page") || targets[0];
if (!page) throw new Error("no CDP page target");

const ws = new WebSocket(page.webSocketDebuggerUrl);
let seq = 0;
const pending = new Map();
const events = [];

function send(method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params }));
  });
}

ws.addEventListener("message", (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const p = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) p.reject(new Error(JSON.stringify(msg.error)));
    else p.resolve(msg.result);
  } else if (msg.method === "Runtime.exceptionThrown") {
    events.push({ type: "exception", text: msg.params.exceptionDetails?.text, desc: msg.params.exceptionDetails?.exception?.description?.slice(0, 500) });
  } else if (msg.method === "Runtime.consoleAPICalled") {
    events.push({ type: "console", level: msg.params.type, args: msg.params.args.map((a) => a.value ?? a.description).join(" ").slice(0, 500) });
  }
});

await new Promise((resolve, reject) => {
  ws.addEventListener("open", resolve);
  ws.addEventListener("error", reject);
});
await send("Runtime.enable");
await send("Log.enable");

async function evalJS(expression, awaitPromise = true) {
  const r = await send("Runtime.evaluate", {
    expression,
    returnByValue: true,
    awaitPromise,
    userGesture: true,
  });
  if (r.exceptionDetails) {
    throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
  }
  return r.result?.value;
}

const script = process.argv[2];
if (!script) {
  console.log(JSON.stringify({ targets: targets.length, title: page.title, url: page.url, events }, null, 2));
  ws.close();
  process.exit(0);
}

try {
  const value = await evalJS(script);
  console.log(JSON.stringify({ ok: true, value, events }, null, 2));
} catch (e) {
  console.log(JSON.stringify({ ok: false, error: e.message, events }, null, 2));
  process.exitCode = 1;
}
ws.close();
