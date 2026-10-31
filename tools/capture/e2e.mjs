// Browser smoke test against a running server: two users in one blank room
// build a NAND circuit by drag-and-drop wiring, see each other's edits and
// cursors, copy/paste, undo, and check live signal values. Exits non-zero
// on the first failed expectation.
//
//   node e2e.mjs [screenshot.png]
import { chromium } from "playwright";

const base = process.env.NANDBOX_URL ?? "http://127.0.0.1:8092";
const shot = process.argv[2];
const browser = await chromium.launch({ channel: "msedge" });
const fail = (msg) => {
  console.error("FAIL:", msg);
  process.exit(1);
};
const check = (cond, msg) => (cond ? console.log("ok  ", msg) : fail(msg));

const alice = await browser.newPage({ viewport: { width: 1200, height: 700 } });
alice.on("pageerror", (e) => fail(`page error: ${e.message}`));
await alice.goto(`${base}/?template=blank`);
await alice.waitForURL(/\/r\/[a-z0-9]+/);
const room = alice.url();
const bob = await browser.newPage({ viewport: { width: 1200, height: 700 } });
await bob.goto(room);
await alice.waitForTimeout(1200);

check((await alice.locator(".peer").count()) === 2, "both users appear in the presence list");

// Alice adds a switch and a NAND from the palette.
await alice.getByRole("button", { name: "Switch", exact: true }).click();
await alice.getByRole("button", { name: "NAND", exact: true }).click();
await alice.waitForTimeout(400);
const geometry = await alice.evaluate(() => window.nandbox.parts());
check(geometry.length === 2, "two parts were added");
await bob.waitForTimeout(300);
check((await bob.evaluate(() => window.nandbox.parts())).length === 2, "bob sees alice's parts");

// Drag from the switch output to both NAND inputs.
const sw = geometry.find((p) => p.kind === "switch");
const nand = geometry.find((p) => p.kind === "nand");
const drag = async (from, to) => {
  await alice.mouse.move(from.x, from.y);
  await alice.mouse.down();
  await alice.mouse.move((from.x + to.x) / 2, (from.y + to.y) / 2, { steps: 4 });
  await alice.mouse.move(to.x, to.y, { steps: 4 });
  await alice.mouse.up();
};
await drag(sw.pins.q, nand.pins.a);
await drag(sw.pins.q, nand.pins.b);
await alice.waitForTimeout(500);
check((await alice.evaluate(() => window.nandbox.wires())) === 2, "two wires drawn by dragging pin to pin");
check((await bob.evaluate(() => window.nandbox.wires())) === 2, "bob sees the wires");
const low = await alice.evaluate(() => window.nandbox.pinValue("nand", "y"));
check(low === 1, "NAND(0,0) output is live and high");
// Toggle the switch: NAND(1,1) = 0.
await alice.mouse.click(sw.center.x, sw.center.y);
await alice.waitForTimeout(400);
check((await alice.evaluate(() => window.nandbox.pinValue("nand", "y"))) === 0, "toggling the switch drives the NAND low");

// Select both with a shift-drag marquee, copy and paste.
await alice.keyboard.down("Shift");
await alice.mouse.move(Math.min(sw.box.x0, nand.box.x0) - 20, Math.min(sw.box.y0, nand.box.y0) - 20);
await alice.mouse.down();
await alice.mouse.move(Math.max(sw.box.x1, nand.box.x1) + 20, Math.max(sw.box.y1, nand.box.y1) + 20, { steps: 5 });
await alice.mouse.up();
await alice.keyboard.up("Shift");
await alice.keyboard.press("Control+c");
await alice.keyboard.press("Control+v");
await alice.waitForTimeout(500);
check((await alice.evaluate(() => window.nandbox.parts())).length === 4, "paste duplicated both parts");
check((await alice.evaluate(() => window.nandbox.wires())) === 4, "paste duplicated the wires between them");
check((await bob.evaluate(() => window.nandbox.wires())) === 4, "bob sees the paste");

// Undo the paste.
await alice.keyboard.press("Control+z");
await alice.waitForTimeout(500);
check((await bob.evaluate(() => window.nandbox.parts())).length === 2, "undo removes the pasted parts for everyone");

// Bob's cursor shows up on Alice's canvas.
await bob.mouse.move(300, 300);
await bob.mouse.move(320, 310);
await alice.waitForTimeout(400);
check((await alice.evaluate(() => window.nandbox.peerCursors())) >= 1, "alice sees bob's cursor");

// Turn the NAND into a chip of its own, then dive into it.
alice.once("dialog", (d) => d.accept("Inverter"));
await alice.mouse.click(nand.center.x + 5, nand.center.y);
await alice.keyboard.press("Control+g");
await alice.waitForTimeout(600);
const world = await alice.evaluate(() => window.nandbox.parts());
check(world.length === 2 && world.some((p) => p.kind === "chip"), "the NAND became a chip instance");
check((await alice.evaluate(() => window.nandbox.wires())) === 1, "the two crossing wires share one interface pin");
check((await bob.evaluate(() => window.nandbox.parts())).some((p) => p.kind === "chip"), "bob sees the new chip");
check(await alice.evaluate(() => window.nandbox.dive("Inverter")), "dive into the user-made chip");
await alice.waitForTimeout(1500);
check((await alice.evaluate(() => window.nandbox.parts())).length === 2, "inside: the NAND and its input pin");
check((await alice.evaluate(() => window.nandbox.wires())) === 2, "inside: the input pin feeds both NAND inputs");
check((await alice.evaluate(() => window.nandbox.pinValue("nand", "y"))) === 0, "signals are live inside the new chip");
if (shot) await alice.screenshot({ path: shot });
await browser.close();
console.log("e2e passed");
