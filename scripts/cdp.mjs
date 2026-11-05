// cdp.mjs — minimal Chrome DevTools Protocol driver for DSH baseline capture.
//
// Usage:
//   node scripts/cdp.mjs shot  --out baseline/screenshots/x.png [--url /] [--wait 4000]
//   node scripts/cdp.mjs eval  --js "document.title"
//   node scripts/cdp.mjs dump  --out /tmp/controls.json
//   node scripts/cdp.mjs click --text "Plugins" --out baseline/screenshots/y.png
//
// It mints a DSH browser-session cookie from ~/.dsh/.credentials.yaml, launches
// a headless Chromium with remote debugging, sets the cookie, and drives the
// page. Pure Node (no npm deps). Chrome is the Playwright-pinned Chromium.

import { spawn } from "node:child_process";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { createHash, createHmac } from "node:crypto";
import { dirname } from "node:path";

const CHROME = "/root/.cache/ms-playwright/chromium-1248/chrome-linux64/chrome";
const PORT = Number(process.env.DSH_CDP_PORT || 9444);
const BASE = process.env.DSH_BASE || "http://10.88.7.230:8088";
const AUTHORITY = process.env.DSH_AUTHORITY || "10.88.7.230:8088";
const CREDENTIALS = process.env.DSH_CREDENTIALS || `${process.env.HOME}/.dsh/.credentials.yaml`;
const SECRET = readSecret();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function readSecret() {
  const yaml = readFileSync(CREDENTIALS, "utf8");
  const m = yaml.match(/client-connection\/browser-session:[\s\S]*?secret:\s*(\S+)/);
  if (!m) throw new Error("browser-session secret not found in ~/.dsh/.credentials.yaml");
  return Buffer.from(m[1], "base64url");
}

function sessionCookie() {
  const now = Date.now();
  const payload = { version: 1, authority: AUTHORITY, issuedAt: now, expiresAt: now + 7 * 24 * 3600 * 1000 };
  const body = Buffer.from(JSON.stringify(payload)).toString("base64url");
  const sig = createHmac("sha256", SECRET).update(body).digest("base64url");
  const name = "dsh-auth-" + createHash("sha256").update(AUTHORITY).digest("base64url");
  return { name, value: `v1.${body}.${sig}` };
}

class CDP {
  constructor(ws) {
    this.ws = ws;
    this.id = 0;
    this.pending = new Map();
    this.listeners = new Map();
  }
  static async connect(url) {
    const ws = new WebSocket(url);
    await new Promise((res, rej) => {
      ws.onopen = res;
      ws.onerror = () => rej(new Error("websocket error: " + url));
    });
    const cdp = new CDP(ws);
    ws.onmessage = (ev) => {
      const msg = JSON.parse(typeof ev.data === "string" ? ev.data : ev.data.toString());
      if (msg.id !== undefined) {
        const p = cdp.pending.get(msg.id);
        if (!p) return;
        cdp.pending.delete(msg.id);
        msg.error ? p.reject(new Error(msg.error.message)) : p.resolve(msg.result);
      } else {
        for (const fn of cdp.listeners.get(msg.method) ?? []) fn(msg.params);
      }
    };
    return cdp;
  }
  send(method, params = {}) {
    const id = ++this.id;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.ws.send(JSON.stringify({ id, method, params }));
    });
  }
  on(method, fn) {
    const arr = this.listeners.get(method) ?? [];
    arr.push(fn);
    this.listeners.set(method, arr);
  }
  once(method, timeout = 15000) {
    return new Promise((resolve, reject) => {
      const t = setTimeout(() => reject(new Error("timeout waiting for " + method)), timeout);
      const fn = (params) => { clearTimeout(t); resolve(params); };
      const arr = this.listeners.get(method) ?? [];
      arr.push(fn);
      this.listeners.set(method, arr);
    });
  }
}

async function launch() {
  const child = spawn(CHROME, [
    "--headless=new", "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
    "--disable-dev-shm-usage", "--no-first-run", "--no-default-browser-check",
    `--remote-debugging-port=${PORT}`, `--user-data-dir=${process.env.DSH_USERDATA || "/tmp/dsh-cdp-profile"}`,
    "--window-size=1600,1000", "about:blank",
  ], { stdio: "ignore" });
  let info;
  for (let i = 0; i < 60; i++) {
    try {
      const res = await fetch(`http://127.0.0.1:${PORT}/json/list`);
      const list = await res.json();
      const page = list.find((t) => t.type === "page");
      if (page) { info = page; break; }
    } catch {}
    await sleep(250);
  }
  if (!info) throw new Error("chrome did not expose a page target");
  const cdp = await CDP.connect(info.webSocketDebuggerUrl);
  await cdp.send("Page.enable");
  await cdp.send("Runtime.enable");
  await cdp.send("Network.enable");
  await cdp.send("Emulation.setDeviceMetricsOverride", { width: 1600, height: 1000, deviceScaleFactor: 1, mobile: false });
  const cookie = sessionCookie();
  await cdp.send("Network.setCookie", {
    name: cookie.name, value: cookie.value, url: BASE + "/",
    path: "/", httpOnly: true, sameSite: "Strict",
  });
  return { cdp, child };
}

async function goto(cdp, path = "/", wait = 3500) {
  const loaded = cdp.once("Page.loadEventFired");
  await cdp.send("Page.navigate", { url: BASE + path });
  await loaded.catch(() => {});
  await sleep(wait);
}

async function shot(cdp, out) {
  const { data } = await cdp.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  mkdirSync(dirname(out), { recursive: true });
  writeFileSync(out, Buffer.from(data, "base64"));
  console.log(out);
}

async function evaluate(cdp, expression, awaitPromise = true) {
  const r = await cdp.send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.text + " " + JSON.stringify(r.exceptionDetails.exception?.description));
  return r.result.value;
}


// ---- scroll + behavior helpers -------------------------------------------------
const SCROLL_ROOT = `(document.querySelector('[role="dialog"], [role="alertdialog"], dialog') || document)`;

const SCROLL_STEP_JS = (frac) => `(() => {
  const root = ${SCROLL_ROOT};
  const cands = [...root.querySelectorAll('*')].filter((el) => {
    const cs = getComputedStyle(el);
    if (!/(auto|scroll)/.test(cs.overflowY)) return false;
    if (el.scrollHeight <= el.clientHeight + 8) return false;
    const r = el.getBoundingClientRect();
    return r.width > 40 && r.height > 120 && r.bottom > 0 && r.top < innerHeight;
  });
  cands.sort((a, b) => (b.clientWidth * b.clientHeight) - (a.clientWidth * a.clientHeight));
  for (const el of cands.slice(0, 8)) {
    const before = el.scrollTop;
    const max = el.scrollHeight - el.clientHeight;
    const target = Math.min(max, before + el.clientHeight * ${frac});
    if (target === before) continue;
    el.scrollTop = target;
    if (el.scrollTop !== before) {
      return { top: el.scrollTop, max, changed: true };
    }
  }
  return null;
})()`;

const SCROLL_TOP_JS = `(() => {
  const root = ${SCROLL_ROOT};
  const cands = [...root.querySelectorAll('*')].filter((el) => {
    const cs = getComputedStyle(el);
    return /(auto|scroll)/.test(cs.overflowY) && el.scrollHeight > el.clientHeight + 8;
  });
  let n = 0;
  for (const el of cands) { if (el.scrollTop !== 0) { el.scrollTop = 0; n++; } }
  return { reset: n };
})()`;

const SNAP_JS = `(() => {
  const controls = [...document.querySelectorAll('button, a, input, textarea, select, [role="button"], [role="tab"], [role="menuitem"], [role="menuitemradio"], [role="switch"], [role="checkbox"], [role="combobox"], [role="option"], [contenteditable="true"]')]
    .map((el) => {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) return null;
      const text = (el.innerText || el.value || el.getAttribute('aria-label') || el.getAttribute('title') || '').trim().replace(/\\s+/g, ' ').slice(0, 100);
      return { tag: el.tagName.toLowerCase(), text, disabled: el.hasAttribute('disabled') };
    }).filter(Boolean);
  return {
    url: location.href,
    dialogs: [...document.querySelectorAll('[role="dialog"], [role="alertdialog"], dialog')].length,
    controls,
    text: document.body.innerText.replace(/\\s+/g, ' ').slice(0, 5000),
  };
})()`;


const CSS_PROPS = ["color","background-color","background-image","border-top-width","border-top-color","border-top-style","border-bottom-width","border-bottom-color","border-left-width","border-right-width","border-radius","box-shadow","font-family","font-size","font-weight","font-style","line-height","letter-spacing","text-transform","text-align","display","position","flex-direction","align-items","justify-content","flex-wrap","gap","row-gap","column-gap","padding-top","padding-right","padding-bottom","padding-left","margin-top","margin-right","margin-bottom","margin-left","opacity","overflow-x","overflow-y","cursor","transition-duration","transition-timing-function","transform","z-index"];

const CSS_JS = `(() => {
  const out = [];
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    const cs = getComputedStyle(el);
    const st = {};
    for (const p of ${JSON.stringify(CSS_PROPS)}) { const v = cs.getPropertyValue(p); if (v && v !== 'none' && v !== 'normal' && v !== 'auto') st[p] = v; }
    const cls = (typeof el.className === 'string' ? el.className : '');
    out.push({ tag: el.tagName.toLowerCase(), role: el.getAttribute('role') || '', cls: cls.slice(0, 160), text: (el.innerText || '').trim().replace(/\s+/g, ' ').slice(0, 100), rect: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)], st });
  }
  return out;
})()`;

function aggregateCss(elements) {
  const agg = {};
  for (const el of elements) {
    for (const [prop, value] of Object.entries(el.st)) {
      if (!agg[prop]) agg[prop] = {};
      const bucket = agg[prop][value] || (agg[prop][value] = { count: 0, examples: [] });
      bucket.count++;
      if (bucket.examples.length < 4) bucket.examples.push(el.tag + (el.text ? ' "' + el.text.slice(0, 24) + '"' : ''));
    }
  }
  return agg;
}

// Interactive controls on the current view.
const DUMP_JS = `(() => {
  const sel = 'button, a, input, textarea, select, [role="button"], [role="tab"], [role="menuitem"], [role="menuitemradio"], [role="switch"], [role="checkbox"], [role="combobox"], [role="option"], [contenteditable="true"], [tabindex]';
  const out = [];
  for (const el of document.querySelectorAll(sel)) {
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    const txt = (el.innerText || el.value || el.getAttribute('aria-label') || el.getAttribute('title') || '').trim().replace(/\\s+/g, ' ').slice(0, 120);
    out.push({
      tag: el.tagName.toLowerCase(), role: el.getAttribute('role') || '',
      text: txt, label: el.getAttribute('aria-label') || '', title: el.getAttribute('title') || '',
      disabled: el.hasAttribute('disabled') || el.getAttribute('aria-disabled') === 'true',
      x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height),
      cls: (el.className && typeof el.className === 'string' ? el.className : '').slice(0, 80),
    });
  }
  return { title: document.title, url: location.href, viewport: [innerWidth, innerHeight], controls: out, text: document.body.innerText.slice(0, 4000) };
})()`;

async function findByText(cdp, text) {
  const js = `(() => {
    const wanted = ${JSON.stringify(text)};
    const nodes = [...document.querySelectorAll('button, a, [role="button"], [role="tab"], [role="menuitem"], [role="menuitemradio"], [role="option"], span, div')];
    const hit = nodes.find(el => {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) return false;
      const t = (el.innerText || '').trim();
      return t === wanted || el.getAttribute('aria-label') === wanted || el.getAttribute('title') === wanted;
    });
    if (!hit) return null;
    const r = hit.getBoundingClientRect();
    return { x: r.x + r.width/2, y: r.y + r.height/2, text: (hit.innerText||'').trim().slice(0,80) };
  })()`;
  return evaluate(cdp, js);
}

async function clickAt(cdp, x, y) {
  for (const type of ["mouseMoved", "mousePressed", "mouseReleased"]) {
    await cdp.send("Input.dispatchMouseEvent", { type, x, y, button: "left", clickCount: 1 });
  }
}

async function main() {
  const [cmd, ...rest] = process.argv.slice(2);
  const args = Object.fromEntries(rest.map((v, i) => (v.startsWith("--") ? [v.slice(2), rest[i + 1]] : null)).filter(Boolean));
  const { cdp, child } = await launch();
  try {
    if (cmd === "shot") {
      await goto(cdp, args.url || "/", Number(args.wait || 3500));
      await shot(cdp, args.out || "baseline/screenshots/shot.png");
    } else if (cmd === "dump") {
      await goto(cdp, args.url || "/", Number(args.wait || 3500));
      const data = await evaluate(cdp, DUMP_JS);
      mkdirSync(dirname(args.out || "/tmp/dump.json"), { recursive: true });
      writeFileSync(args.out || "/tmp/dump.json", JSON.stringify(data, null, 2));
      console.log((args.out || "/tmp/dump.json") + "  controls=" + data.controls.length);
    } else if (cmd === "eval") {
      await goto(cdp, args.url || "/", Number(args.wait || 3000));
      console.log(JSON.stringify(await evaluate(cdp, args.js), null, 2));
    } else if (cmd === "click") {
      await goto(cdp, args.url || "/", Number(args.wait || 3500));
      const hit = await findByText(cdp, args.text);
      if (!hit) throw new Error("not found: " + args.text);
      console.log("click", JSON.stringify(hit));
      await clickAt(cdp, hit.x, hit.y);
      await sleep(Number(args.wait2 || 1200));
      if (args.out) await shot(cdp, args.out);
    } else if (cmd === "seq") {
      const steps = JSON.parse(readFileSync(args.file, "utf8"));
      for (const st of steps) {
        if (st.act === "goto") await goto(cdp, st.url || "/", st.wait ?? 3000);
        else if (st.act === "wait") await sleep(st.ms ?? 1000);
        else if (st.act === "shot") await shot(cdp, st.out);
        else if (st.act === "dump") {
          const data = await evaluate(cdp, DUMP_JS);
          mkdirSync(dirname(st.out), { recursive: true });
          writeFileSync(st.out, JSON.stringify(data, null, 2));
          console.log(st.out + "  controls=" + data.controls.length);
        } else if (st.act === "click") {
          const hit = await findByText(cdp, st.text);
          if (!hit) { console.error("MISS " + st.text); if (st.optional) continue; throw new Error("not found: " + st.text); }
          await clickAt(cdp, hit.x, hit.y);
          await sleep(st.wait ?? 900);
          console.log("clicked " + st.text);
        } else if (st.act === "clickxy") {
          await clickAt(cdp, st.x, st.y);
          await sleep(st.wait ?? 900);
        } else if (st.act === "key") {
          for (const type of ["keyDown", "keyUp"]) {
            await cdp.send("Input.dispatchKeyEvent", { type, key: st.key, code: st.code || st.key, windowsVirtualKeyCode: st.vk || 0 });
          }
          await sleep(st.wait ?? 500);
        } else if (st.act === "scrollscan") {
          const outDir = st.outDir || "baseline/screenshots";
          const dumpDir = st.dumpDir || "baseline/controls";
          mkdirSync(outDir, { recursive: true });
          mkdirSync(dumpDir, { recursive: true });
          await evaluate(cdp, SCROLL_TOP_JS);
          await sleep(400);
          let i = 0;
          let last = -1;
          while (i < (st.steps || 8)) {
            await shot(cdp, `${outDir}/${st.prefix}-s${i}.png`);
            const data = await evaluate(cdp, DUMP_JS);
            const step = await evaluate(cdp, SCROLL_STEP_JS(st.overlap ?? 0.85));
            writeFileSync(`${dumpDir}/${st.prefix}-s${i}.json`, JSON.stringify({ scroll: step, ...data }, null, 2));
            console.log(`${st.prefix}-s${i} controls=${data.controls.length} scroll=${step ? step.top + "/" + step.max : "none"}`);
            if (!step) break;
            if (step.top === last) break;
            last = step.top;
            await sleep(st.wait ?? 700);
            i++;
          }
        } else if (st.act === "probe") {
          const before = await evaluate(cdp, SNAP_JS);
          const hit = await findByText(cdp, st.text);
          if (!hit) { console.error("MISS " + st.text); if (st.optional) continue; throw new Error("not found: " + st.text); }
          await clickAt(cdp, hit.x, hit.y);
          await sleep(st.wait ?? 1200);
          const after = await evaluate(cdp, SNAP_JS);
          let dialogs = 0;
          try { dialogs = await evaluate(cdp, "document.querySelectorAll('[role=dialog],[role=alertdialog],dialog').length"); } catch {}
          const rec = {
            control: st.text,
            urlBefore: before.url, urlAfter: after.url,
            dialogBefore: before.dialogs, dialogAfter: after.dialogs,
            textChanged: before.text !== after.text,
            textAfterHead: after.text.slice(0, 600),
          };
          mkdirSync("baseline/behavior", { recursive: true });
          writeFileSync(`${st.log || "baseline/behavior/probe.jsonl"}`, JSON.stringify(rec) + "\n", { flag: "a" });
          console.log("probe " + st.text + " textChanged=" + rec.textChanged + " dialogs " + before.dialogs + "->" + after.dialogs);
          if (st.shot) await shot(cdp, st.shot);
        } else if (st.act === "cssscan") {
          const elements = await evaluate(cdp, CSS_JS);
          const agg = aggregateCss(elements);
          mkdirSync(dirname(st.out || "baseline/css/scan.json"), { recursive: true });
          writeFileSync(st.out || "baseline/css/scan.json", JSON.stringify({ screen: st.name || st.out, elements, aggregate: agg }, null, 2));
          console.log((st.out || "baseline/css/scan.json") + " elements=" + elements.length + " props=" + Object.keys(agg).length);
        } else if (st.act === "probescan3") {
          // Per-control probe with a clean reset before every click.
          const runPre = async (steps) => {
            for (const p of steps || []) {
              if (p.act === "goto") await goto(cdp, p.url || "/", p.wait ?? 4000);
              else if (p.act === "wait") await sleep(p.ms ?? 1000);
              else if (p.act === "clickxy") { await clickAt(cdp, p.x, p.y); await sleep(p.wait ?? 800); }
              else if (p.act === "click") {
                const h = await findByText(cdp, p.text);
                if (h) { await clickAt(cdp, h.x, h.y); await sleep(p.wait ?? 900); }
              } else if (p.act === "key") {
                for (const type of ["keyDown", "keyUp"]) await cdp.send("Input.dispatchKeyEvent", { type, key: p.key, code: p.code || p.key, windowsVirtualKeyCode: p.vk || 0 });
                await sleep(p.wait ?? 400);
              } else if (p.act === "type") { await cdp.send("Input.insertText", { text: p.text }); await sleep(p.wait ?? 400); }
            }
          };
          const seen = new Set();
          let done = 0;
          while (done < (st.limit || 30)) {
            await runPre(st.pre);
            const data = await evaluate(cdp, DUMP_JS);
            const ctls = data.controls.filter((c) => c.x < 1600 && c.y < 1000 && c.text && !(st.skip || []).some((x) => c.text.toLowerCase().includes(x)));
            const next = ctls.find((c) => !seen.has(c.tag + "|" + c.text));
            if (!next) { console.log("probescan3: no unprobed controls left (done=" + done + ")"); break; }
            seen.add(next.tag + "|" + next.text);
            const hit = await findByText(cdp, next.text);
            const before = await evaluate(cdp, SNAP_JS);
            if (st.shotDir) await shot(cdp, `${st.shotDir}/${st.prefix}-${String(done).padStart(2, "0")}-before.png`);
            if (hit) await clickAt(cdp, hit.x, hit.y);
            await sleep(st.wait || 900);
            const after = await evaluate(cdp, SNAP_JS);
            if (st.shotDir) await shot(cdp, `${st.shotDir}/${st.prefix}-${String(done).padStart(2, "0")}-after.png`);
            const rec = {
              screen: st.prefix, control: next.text, tag: next.tag, at: [next.x, next.y],
              urlBefore: before.url, urlAfter: after.url,
              dialogBefore: before.dialogs, dialogAfter: after.dialogs,
              controlsBefore: before.controls.length, controlsAfter: after.controls.length,
              textChanged: before.text !== after.text,
              textAfterHead: after.text.slice(0, 500),
            };
            mkdirSync("baseline/behavior", { recursive: true });
            writeFileSync(st.log || "baseline/behavior/probe3.jsonl", JSON.stringify(rec) + "\n", { flag: "a" });
            console.log("probe3 " + JSON.stringify(next.text).slice(0, 44) + " changed=" + rec.textChanged);
            await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
            await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
            await sleep(200);
            done++;
          }
          console.log("probescan3 done=" + done);
        } else if (st.act === "probescan2") {
          const seen = new Set();
          let done = 0;
          while (done < (st.limit || 30)) {
            const data = await evaluate(cdp, DUMP_JS);
            const ctls = data.controls.filter((c) => c.x < 1600 && c.y < 1000 && c.text && !(st.skip || []).some((s2) => c.text.toLowerCase().includes(s2)));
            const next = ctls.find((c) => !seen.has(c.tag + "|" + c.text));
            if (!next) break;
            seen.add(next.tag + "|" + next.text);
            const hit = await findByText(cdp, next.text);
            if (!hit) { done++; continue; }
            const before = await evaluate(cdp, SNAP_JS);
            if (st.shotDir) await shot(cdp, `${st.shotDir}/${st.prefix}-${String(done).padStart(2, "0")}-before.png`);
            await clickAt(cdp, hit.x, hit.y);
            await sleep(st.wait || 900);
            const after = await evaluate(cdp, SNAP_JS);
            if (st.shotDir) await shot(cdp, `${st.shotDir}/${st.prefix}-${String(done).padStart(2, "0")}-after.png`);
            const rec = {
              screen: st.prefix, control: next.text, tag: next.tag, at: [next.x, next.y],
              urlBefore: before.url, urlAfter: after.url,
              dialogBefore: before.dialogs, dialogAfter: after.dialogs,
              controlsBefore: before.controls.length, controlsAfter: after.controls.length,
              textChanged: before.text !== after.text,
              textAfterHead: after.text.slice(0, 500),
            };
            mkdirSync("baseline/behavior", { recursive: true });
            writeFileSync(st.log || "baseline/behavior/probe2.jsonl", JSON.stringify(rec) + "\n", { flag: "a" });
            console.log("probe2 " + JSON.stringify(next.text).slice(0, 44) + " changed=" + rec.textChanged);
            await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
            await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
            await sleep(400);
            const now = await evaluate(cdp, SNAP_JS);
            if (now.url !== before.url) await goto(cdp, st.resetUrl || "/", 3500);
            else { await clickAt(cdp, 1200, 300); await sleep(300); }
            done++;
          }
          console.log("probescan2 done=" + done);
        } else if (st.act === "probescan") {
          const skip = st.skip || [];
          const data = await evaluate(cdp, DUMP_JS);
          const candidates = data.controls.filter((c) => c.x < 1600 && c.y < 1000 && c.text);
          let done = 0;
          for (const c of candidates) {
            if (done >= (st.limit || 12)) break;
            if (skip.some((s) => c.text.toLowerCase().includes(s))) continue;
            const before = await evaluate(cdp, SNAP_JS);
            const hit = await findByText(cdp, c.text);
            if (!hit) continue;
            await clickAt(cdp, hit.x, hit.y);
            await sleep(st.wait ?? 1000);
            const after = await evaluate(cdp, SNAP_JS);
            const rec = {
              control: c.text, tag: c.tag, at: [c.x, c.y],
              urlBefore: before.url, urlAfter: after.url,
              dialogBefore: before.dialogs, dialogAfter: after.dialogs,
              textChanged: before.text !== after.text,
              textAfterHead: after.text.slice(0, 400),
            };
            mkdirSync("baseline/behavior", { recursive: true });
            writeFileSync(st.log || "baseline/behavior/probe.jsonl", JSON.stringify(rec) + "\n", { flag: "a" });
            console.log("probe " + JSON.stringify(c.text).slice(0, 40) + " changed=" + rec.textChanged);
            // reset: escape, dismiss overlays, reload if navigated
            await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
            await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
            await sleep(400);
            const now = await evaluate(cdp, SNAP_JS);
            if (now.url !== before.url) await goto(cdp, st.resetUrl || "/", 3500);
            else await clickAt(cdp, 1200, 300);
            await sleep(300);
            done++;
          }
          console.log("probescan done=" + done);
        } else if (st.act === "viewport") {
          await cdp.send("Emulation.setDeviceMetricsOverride", { width: st.width, height: st.height, deviceScaleFactor: 1, mobile: false });
          await sleep(st.wait ?? 800);
        } else if (st.act === "dark") {
          await cdp.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: st.on ? "dark" : "light" }] });
          await cdp.send("Page.reload");
          await sleep(st.wait ?? 3500);
        } else if (st.act === "type") {
          await cdp.send("Input.insertText", { text: st.text });
          await sleep(st.wait ?? 500);
        } else if (st.act === "eval") {
          console.log(JSON.stringify(await evaluate(cdp, st.js), null, 2));
        } else throw new Error("unknown step " + JSON.stringify(st));
      }
    } else {
      throw new Error("unknown command: " + cmd);
    }
  } finally {
    child.kill("SIGKILL");
  }
}

main().catch((e) => { console.error(e.message); process.exit(1); });
