// Captures the README hero frames from a running nandbox server:
// Snake playing on the LED matrix, then a dive from the world into the CPU,
// the ALU, the adder and a full adder's live NAND gates.
//
// A second player ("snakebot") joins the same room over the public WebSocket
// protocol and steers toward the food, which also shows multiplayer presence.
//
//   node capture.mjs <frames-dir> [stills-dir]
//
// Frames are PNGs named f0000.png...; make-gif.mjs turns them into the GIF.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright";

const [framesDir = "frames", stillsDir = "stills"] = process.argv.slice(2);
const base = process.env.NANDBOX_URL ?? "http://127.0.0.1:8092";
mkdirSync(framesDir, { recursive: true });
mkdirSync(stillsDir, { recursive: true });

const browser = await chromium.launch({ channel: "msedge" });
const page = await browser.newPage({ viewport: { width: 1200, height: 700 }, deviceScaleFactor: 1 });
page.on("pageerror", (e) => console.error("page error:", e.message));
await page.goto(`${base}/`);
await page.waitForURL(/\/r\/[a-z0-9]+/);
const roomId = new URL(page.url()).pathname.split("/").pop();
console.log("room", roomId);

// --- the bot player ---------------------------------------------------------
const UP = 1, DOWN = 2, LEFT = 4, RIGHT = 8;
const ws = new WebSocket(`${base.replace("http", "ws")}/api/rooms/${roomId}/ws`);
ws.binaryType = "arraybuffer";
let heading = RIGHT;
let lastHead = -1;
ws.onopen = () => ws.send(JSON.stringify({ type: "hello", name: "snakebot" }));
const step = (pos, d) => {
  const row = pos & 0xf0;
  if (d === UP) return (pos - 16) & 0xff;
  if (d === DOWN) return (pos + 16) & 0xff;
  if (d === LEFT) return row | ((pos - 1) & 0x0f);
  return row | ((pos + 1) & 0x0f);
};
const wrap = (d) => (d > 8 ? d - 16 : d < -8 ? d + 16 : d);
const opposite = { [UP]: DOWN, [DOWN]: UP, [LEFT]: RIGHT, [RIGHT]: LEFT };
ws.onmessage = (ev) => {
  if (typeof ev.data === "string") return;
  const b = new Uint8Array(ev.data);
  if (b[0] !== 2) return;
  const px = b.subarray(1, 257);
  const head = px.indexOf(6);
  const food = px.indexOf(5);
  if (head < 0 || food < 0 || head === lastHead) return;
  if (lastHead >= 0) {
    for (const d of [UP, DOWN, LEFT, RIGHT]) if (step(lastHead, d) === head) heading = d;
  }
  lastHead = head;
  const dx = wrap((food & 15) - (head & 15));
  const dy = wrap((food >> 4) - (head >> 4));
  const prefs = [];
  if (dx) prefs.push(dx > 0 ? RIGHT : LEFT);
  if (dy) prefs.push(dy > 0 ? DOWN : UP);
  prefs.push(heading, UP, RIGHT, DOWN, LEFT);
  // Take the first preferred move whose region is roomy enough to survive in;
  // otherwise the move with the most reachable free cells.
  const length = px.reduce((n, v) => n + (v >= 1 && v <= 4 ? 1 : 0), 1);
  let best = 0;
  let bestRoom = -1;
  for (const d of prefs) {
    if (d === opposite[heading]) continue;
    const next = step(head, d);
    const v = px[next];
    if (v !== 0 && v !== 5) continue;
    const room = reachable(px, next);
    if (room > length + 4) {
      best = d;
      break;
    }
    if (room > bestRoom) [best, bestRoom] = [d, room];
  }
  if (best) ws.send(JSON.stringify({ type: "joy", bits: best }));
};
function reachable(px, start) {
  const seen = new Uint8Array(256);
  const queue = [start];
  seen[start] = 1;
  for (let i = 0; i < queue.length; i++) {
    for (const d of [UP, DOWN, LEFT, RIGHT]) {
      const q = step(queue[i], d);
      if (!seen[q] && (px[q] === 0 || px[q] === 5)) {
        seen[q] = 1;
        queue.push(q);
      }
    }
  }
  return queue.length;
}
// Wander the bot's cursor around the CPU block.
let t = 0;
const cursor = setInterval(() => {
  t += 0.08;
  ws.readyState === 1 && ws.send(JSON.stringify({ type: "cursor", x: 22 + 9 * Math.cos(t), y: 9 + 6 * Math.sin(1.3 * t), path: [] }));
}, 60);

// --- recording ---------------------------------------------------------------
let n = 0;
async function record(ms) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    await page.screenshot({ path: join(framesDir, `f${String(n++).padStart(4, "0")}.png`) });
    await page.waitForTimeout(30); // let animation frames run between captures
  }
}
let depth = 1;
async function dive(label) {
  const ok = await page.evaluate((l) => window.nandbox.dive(l), label);
  if (!ok) throw new Error(`no chip labelled ${label}`);
  depth++;
}
// settle records until the dive animation has swapped to the inner chip.
async function settle(ms) {
  await record(ms);
  await page.waitForFunction((n) => document.querySelectorAll("#crumbs .crumb").length >= n, depth, { timeout: 10000 });
}

await page.waitForTimeout(2500); // let the snake appear and the bot start chasing
await page.screenshot({ path: join(stillsDir, "world.png") });
await record(4500);
await dive("CPU");
await settle(3200);
await page.screenshot({ path: join(stillsDir, "cpu.png") });
await dive("alu");
await settle(2600);
await page.screenshot({ path: join(stillsDir, "alu.png") });
await dive("adder");
await settle(1800);
await dive("fa0");
await record(4200);
await page.screenshot({ path: join(stillsDir, "fulladder.png") });
console.log(`captured ${n} frames`);

clearInterval(cursor);
ws.close();
await browser.close();
