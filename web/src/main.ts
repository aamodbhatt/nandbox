// nandbox editor: connects to a room, renders the current chip instance with
// live signals, and turns mouse/keyboard input into edit ops, presence and
// joystick input.
import { disassemble } from "./isa";
import { apply, type ChipDef, type Design, type Op, type Part, type Point, shapeOf, type Wire } from "./model";
import { type Camera, draw, MATRIX_PALETTE, type Peer, type Scene, segmentDigit, wirePath } from "./render";

interface SimState {
  running: boolean;
  hz: number;
  actualHz: number;
  cycles: number;
  error: { kind: string; msg: string; locations?: { path: string[]; part: string }[] } | null;
  stats: { nands: number; dffs: number; memories: number; levels: number };
}

const $ = <T extends HTMLElement>(id: string): T => {
  const el = document.getElementById(id);
  if (!el) throw new Error(`missing #${id}`);
  return el as T;
};

const canvas = $<HTMLCanvasElement>("canvas");
const ctx = canvas.getContext("2d") as CanvasRenderingContext2D;
const screen = $<HTMLCanvasElement>("screen");
const sctx = screen.getContext("2d") as CanvasRenderingContext2D;

const state = {
  design: null as Design | null,
  me: null as Peer | null,
  peers: [] as Peer[],
  path: [] as string[],
  cam: { x: -4, y: -4, scale: 12 } as Camera,
  selected: new Set<string>(),
  watch: { vid: 0, wires: [] as { id: string; w: number }[], pins: [] as { part: string; pin: string; w: number }[] },
  wireVals: new Map<string, number>(),
  pinVals: new Map<string, number>(),
  matrix: null as Uint8Array | null,
  segs: [] as number[],
  leds: 0,
  pc: -1,
  ir: 0,
  sim: null as SimState | null,
  pending: new Set<string>(),
  joy: 0,
  dirty: true,
};

let ws: WebSocket | null = null;
let cidSeq = 0;

function send(msg: object): void {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
}

function currentChip(): ChipDef | null {
  const d = state.design;
  if (!d) return null;
  let chip = d.chips[d.root];
  for (const id of state.path) {
    const p = chip?.parts[id];
    if (!p || p.kind !== "chip" || !p.chip) return null;
    chip = d.chips[p.chip];
  }
  return chip ?? null;
}

// ---- edits ---------------------------------------------------------------

function edit(op: Op): void {
  const d = state.design;
  if (!d) return;
  const cid = `${state.me?.id ?? "x"}-${++cidSeq}`;
  try {
    apply(d, op); // optimistic: show it now, the server confirms or rejects
  } catch (e) {
    toast(String(e));
    return;
  }
  state.pending.add(cid);
  send({ type: "op", cid, op });
  state.dirty = true;
  sendView();
}

function newId(prefix: string): string {
  return `${prefix}${Date.now().toString(36)}${Math.floor(Math.random() * 1296).toString(36)}`;
}

// ---- networking ------------------------------------------------------------

async function roomId(): Promise<string> {
  const m = location.pathname.match(/^\/r\/([a-z0-9]+)/);
  if (m?.[1]) return m[1];
  const tmpl = new URLSearchParams(location.search).get("template") ?? "snake";
  const res = await fetch("/api/rooms", { method: "POST", body: JSON.stringify({ template: tmpl }) });
  if (!res.ok) throw new Error(await res.text());
  const { id } = (await res.json()) as { id: string };
  history.replaceState(null, "", `/r/${id}`);
  return id;
}

async function connect(): Promise<void> {
  const id = await roomId();
  const proto = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${proto}://${location.host}/api/rooms/${id}/ws`);
  ws.binaryType = "arraybuffer";
  ws.onopen = () => send({ type: "hello", name: localStorage.getItem("nandbox-name") ?? "" });
  ws.onclose = () => {
    setStatus("disconnected - reconnecting...");
    setTimeout(() => void connect(), 1500);
  };
  ws.onmessage = (ev) => {
    if (typeof ev.data === "string") onJSON(JSON.parse(ev.data));
    else onFrame(new Uint8Array(ev.data as ArrayBuffer));
  };
}

function onJSON(m: Record<string, unknown>): void {
  switch (m.type) {
    case "welcome": {
      state.design = m.doc as Design;
      state.me = m.you as Peer;
      state.pending.clear();
      const root = state.design.chips[state.design.root];
      if (root) fitView(root, false);
      renderCrumbs();
      renderPalette();
      setStatus("");
      break;
    }
    case "op": {
      const cid = m.cid as string;
      if (state.pending.has(cid)) {
        state.pending.delete(cid); // already applied locally
      } else if (state.design) {
        try {
          apply(state.design, m.op as Op);
        } catch {
          location.reload(); // diverged: start from the server's copy
        }
      }
      state.dirty = true;
      renderCrumbs();
      renderPalette();
      break;
    }
    case "reject":
      toast(`Edit rejected: ${m.error as string}`);
      location.reload();
      break;
    case "peers":
      state.peers = (m.peers as Peer[]).filter((p) => p.id !== state.me?.id).map((p) => ({ ...p, path: p.path ?? [] }));
      renderPeers();
      break;
    case "cursor": {
      const p = state.peers.find((q) => q.id === m.id);
      if (p) {
        p.x = m.x as number;
        p.y = m.y as number;
        p.path = (m.path as string[]) ?? [];
        state.dirty = true;
      }
      break;
    }
    case "watch":
      state.watch = {
        vid: m.vid as number,
        wires: m.wires as { id: string; w: number }[],
        pins: m.pins as { part: string; pin: string; w: number }[],
      };
      break;
    case "sim":
      state.sim = m as unknown as SimState;
      renderSim();
      break;
  }
}

function onFrame(b: Uint8Array): void {
  if (b[0] === 1) {
    const view = new DataView(b.buffer, b.byteOffset, b.byteLength);
    if (view.getUint32(1, true) !== state.watch.vid) return;
    let bit = 0;
    const read = (w: number): number => {
      let v = 0;
      for (let i = 0; i < w; i++, bit++) if (((b[5 + (bit >> 3)] ?? 0) >> (bit & 7)) & 1) v |= 1 << i;
      return v;
    };
    state.pinVals.clear();
    state.wireVals.clear();
    for (const p of state.watch.pins) state.pinVals.set(`${p.part}.${p.pin}`, read(p.w));
    for (const w of state.watch.wires) state.wireVals.set(w.id, read(w.w));
    state.dirty = true;
  } else if (b[0] === 2) {
    state.matrix = b.slice(1, 257);
    const n = b[257] ?? 0;
    state.segs = Array.from(b.slice(258, 258 + n));
    state.leds = b[258 + n] ?? 0;
    if (b.length >= 262 + n) {
      state.pc = b[259 + n] ?? 0;
      state.ir = ((b[260 + n] ?? 0) << 8) | (b[261 + n] ?? 0);
    }
    renderDock();
    state.dirty = true;
  }
}

// ---- view -----------------------------------------------------------------

function bounds(chip: ChipDef): { x0: number; y0: number; x1: number; y1: number } {
  const d = state.design as Design;
  let x0 = Infinity;
  let y0 = Infinity;
  let x1 = -Infinity;
  let y1 = -Infinity;
  for (const p of Object.values(chip.parts)) {
    const s = shapeOf(d, p);
    x0 = Math.min(x0, p.x);
    y0 = Math.min(y0, p.y);
    x1 = Math.max(x1, p.x + s.w);
    y1 = Math.max(y1, p.y + s.h);
  }
  for (const w of Object.values(chip.wires)) {
    for (const r of w.route ?? []) {
      y1 = Math.max(y1, r.y);
      x1 = Math.max(x1, r.x);
    }
  }
  if (x0 === Infinity) return { x0: -20, y0: -12, x1: 20, y1: 12 };
  return { x0, y0, x1, y1 };
}

function fitCam(chip: ChipDef): Camera {
  const b = bounds(chip);
  const w = canvas.clientWidth;
  const h = canvas.clientHeight;
  const pad = 4;
  const scale = Math.min(w / (b.x1 - b.x0 + 2 * pad), h / (b.y1 - b.y0 + 2 * pad), 40);
  return {
    scale,
    x: (b.x0 + b.x1) / 2 - w / scale / 2,
    y: (b.y0 + b.y1) / 2 - h / scale / 2,
  };
}

let anim: { from: Camera; to: Camera; t0: number; ms: number; done?: () => void } | null = null;

function animateTo(to: Camera, ms: number, done?: () => void): void {
  anim = { from: { ...state.cam }, to, t0: performance.now(), ms, done };
}

function fitView(chip: ChipDef, animated: boolean): void {
  const to = fitCam(chip);
  if (animated) animateTo(to, 380);
  else state.cam = to;
  sendView();
}

let viewTimer = 0;
function sendView(): void {
  window.clearTimeout(viewTimer);
  viewTimer = window.setTimeout(() => {
    const c = state.cam;
    send({
      type: "view",
      path: state.path,
      rect: { X0: c.x, Y0: c.y, X1: c.x + canvas.clientWidth / c.scale, Y1: c.y + canvas.clientHeight / c.scale },
    });
  }, 60);
}

/** dive zooms into a chip instance and then swaps to its inside. */
function dive(part: Part): void {
  const d = state.design;
  if (!d || part.kind !== "chip" || !part.chip) return;
  const inner = d.chips[part.chip];
  if (!inner) return;
  const s = shapeOf(d, part);
  const w = canvas.clientWidth;
  const h = canvas.clientHeight;
  const scale = Math.min(w / s.w, h / s.h) * 0.9;
  const to = { scale, x: part.x + s.w / 2 - w / scale / 2, y: part.y + s.h / 2 - h / scale / 2 };
  animateTo(to, 420, () => {
    state.path = [...state.path, part.id];
    state.selected.clear();
    state.wireVals.clear();
    state.pinVals.clear();
    // Start zoomed out on the inside (the chip box), then settle on a fit.
    const c = fitCam(inner);
    const k = 0.4;
    state.cam = { scale: c.scale * k, x: c.x - ((w / c.scale) * (1 / k - 1)) / 2, y: c.y - ((h / c.scale) * (1 / k - 1)) / 2 };
    animateTo(c, 420);
    renderCrumbs();
    sendView();
  });
}

function goTo(depth: number): void {
  if (depth >= state.path.length) return;
  state.path = state.path.slice(0, depth);
  state.selected.clear();
  const chip = currentChip();
  if (chip) fitView(chip, false);
  renderCrumbs();
}

// ---- hit testing -----------------------------------------------------------

function toWorld(e: MouseEvent): Point {
  const r = canvas.getBoundingClientRect();
  return { x: state.cam.x + (e.clientX - r.left) / state.cam.scale, y: state.cam.y + (e.clientY - r.top) / state.cam.scale };
}

function hitPin(w: Point): { part: Part; pin: string; pos: Point; width: number } | null {
  const chip = currentChip();
  const d = state.design;
  if (!chip || !d) return null;
  const r = Math.max(0.5, 7 / state.cam.scale);
  for (const p of Object.values(chip.parts)) {
    const s = shapeOf(d, p);
    for (const pin of s.pins) {
      const pos = { x: p.x + pin.x, y: p.y + pin.y };
      if (Math.abs(pos.x - w.x) <= r && Math.abs(pos.y - w.y) <= r) return { part: p, pin: pin.name, pos, width: pin.width };
    }
  }
  return null;
}

function hitPart(w: Point): Part | null {
  const chip = currentChip();
  const d = state.design;
  if (!chip || !d) return null;
  const parts = Object.values(chip.parts);
  for (let i = parts.length - 1; i >= 0; i--) {
    const p = parts[i] as Part;
    const s = shapeOf(d, p);
    if (w.x >= p.x && w.x <= p.x + s.w && w.y >= p.y && w.y <= p.y + s.h) return p;
  }
  return null;
}

function hitWire(w: Point): string | null {
  const chip = currentChip();
  const d = state.design;
  if (!chip || !d) return null;
  const tol = Math.max(0.3, 5 / state.cam.scale);
  for (const id of Object.keys(chip.wires)) {
    const pts = wirePath(d, chip, id);
    if (!pts) continue;
    for (let i = 1; i < pts.length; i++) {
      const a = pts[i - 1] as Point;
      const b = pts[i] as Point;
      const inX = w.x >= Math.min(a.x, b.x) - tol && w.x <= Math.max(a.x, b.x) + tol;
      const inY = w.y >= Math.min(a.y, b.y) - tol && w.y <= Math.max(a.y, b.y) + tol;
      if (inX && inY) return id;
    }
  }
  return null;
}

// ---- input -----------------------------------------------------------------

type Gesture =
  | { kind: "pan"; sx: number; sy: number; cam: Camera }
  | { kind: "marquee"; start: Point; to: Point }
  | { kind: "move"; start: Point; parts: { p: Part; x: number; y: number }[]; moved: boolean; press: Part | null }
  | { kind: "wire"; from: { part: Part; pin: string; pos: Point; width: number }; to: Point; ok: boolean };

let gesture: Gesture | null = null;
let hover: { part: string; pin: string } | null = null;

canvas.addEventListener("mousedown", (e) => {
  const w = toWorld(e);
  const pin = e.button === 0 ? hitPin(w) : null;
  if (pin) {
    gesture = { kind: "wire", from: pin, to: w, ok: false };
    return;
  }
  const part = e.button === 0 ? hitPart(w) : null;
  if (part) {
    if (!state.selected.has(part.id)) {
      if (!e.shiftKey) state.selected.clear();
      state.selected.add(part.id);
    }
    const chip = currentChip() as ChipDef;
    const parts = [...state.selected].flatMap((id) =>
      chip.parts[id] ? [{ p: chip.parts[id] as Part, x: (chip.parts[id] as Part).x, y: (chip.parts[id] as Part).y }] : [],
    );
    const press = part.kind === "button" || part.kind === "switch" ? part : null;
    if (press) send({ type: "press", path: state.path, part: press.id, down: true });
    gesture = { kind: "move", start: w, parts, moved: false, press };
    state.dirty = true;
    return;
  }
  const wire = e.button === 0 ? hitWire(w) : null;
  if (!e.shiftKey) state.selected.clear();
  if (wire) state.selected.add(wire);
  gesture =
    e.shiftKey && !wire ? { kind: "marquee", start: w, to: w } : { kind: "pan", sx: e.clientX, sy: e.clientY, cam: { ...state.cam } };
  state.dirty = true;
});

window.addEventListener("mousemove", (e) => {
  const w = toWorld(e);
  if (e.target === canvas) {
    cursorPos = w;
    const h = hitPin(w);
    const next = h ? { part: h.part.id, pin: h.pin } : null;
    if (JSON.stringify(next) !== JSON.stringify(hover)) {
      hover = next;
      state.dirty = true;
    }
  }
  if (!gesture) return;
  if (gesture.kind === "pan") {
    state.cam.x = gesture.cam.x - (e.clientX - gesture.sx) / state.cam.scale;
    state.cam.y = gesture.cam.y - (e.clientY - gesture.sy) / state.cam.scale;
    sendView();
  } else if (gesture.kind === "marquee") {
    gesture.to = w;
    sendView();
  } else if (gesture.kind === "move") {
    const dx = Math.round(w.x - gesture.start.x);
    const dy = Math.round(w.y - gesture.start.y);
    if (dx !== 0 || dy !== 0) gesture.moved = true;
    for (const m of gesture.parts) {
      m.p.x = m.x + dx;
      m.p.y = m.y + dy;
    }
  } else {
    gesture.to = w;
    const target = hitPin(w);
    gesture.ok = target !== null && target.width === gesture.from.width && target.part.id !== gesture.from.part.id;
    if (target) gesture.to = target.pos;
  }
  state.dirty = true;
});

window.addEventListener("mouseup", (e) => {
  const g = gesture;
  gesture = null;
  if (!g) return;
  if (g.kind === "marquee") {
    const chip = currentChip();
    const d = state.design;
    const x0 = Math.min(g.start.x, g.to.x);
    const x1 = Math.max(g.start.x, g.to.x);
    const y0 = Math.min(g.start.y, g.to.y);
    const y1 = Math.max(g.start.y, g.to.y);
    for (const p of chip && d ? Object.values(chip.parts) : []) {
      const s = shapeOf(d as Design, p);
      if (p.x < x1 && p.x + s.w > x0 && p.y < y1 && p.y + s.h > y0) state.selected.add(p.id);
    }
  } else if (g.kind === "move") {
    if (g.press) send({ type: "press", path: state.path, part: g.press.id, down: false });
    if (g.moved) {
      const chip = currentChip() as ChipDef;
      const ops: Op[] = g.parts.map((m) => {
        const x = m.p.x;
        const y = m.p.y;
        m.p.x = m.x; // restore, then apply through the op log
        m.p.y = m.y;
        return { t: "movePart", chip: chip.id, id: m.p.id, x, y };
      });
      edit(ops.length === 1 ? (ops[0] as Op) : { t: "batch", ops });
    }
  } else if (g.kind === "wire") {
    const target = hitPin(toWorld(e));
    if (target && g.ok) {
      const chip = currentChip() as ChipDef;
      const fromOut = shapeOf(state.design as Design, g.from.part).pins.find((p) => p.name === g.from.pin)?.dir === 1;
      const a = { part: g.from.part.id, pin: g.from.pin };
      const b = { part: target.part.id, pin: target.pin };
      edit({ t: "addWire", chip: chip.id, wire: { id: newId("w"), from: fromOut ? a : b, to: fromOut ? b : a } });
    }
  }
  state.dirty = true;
});

canvas.addEventListener("dblclick", (e) => {
  const part = hitPart(toWorld(e));
  if (part?.kind === "chip") dive(part);
});

canvas.addEventListener(
  "wheel",
  (e) => {
    e.preventDefault();
    const before = toWorld(e);
    const k = Math.exp(-e.deltaY * 0.0015);
    state.cam.scale = Math.min(80, Math.max(1.5, state.cam.scale * k));
    const after = toWorld(e);
    state.cam.x += before.x - after.x;
    state.cam.y += before.y - after.y;
    state.dirty = true;
    sendView();
  },
  { passive: false },
);

const JOY_KEYS: Record<string, number> = {
  ArrowUp: 1,
  KeyW: 1,
  ArrowDown: 2,
  KeyS: 2,
  ArrowLeft: 4,
  KeyA: 4,
  ArrowRight: 8,
  KeyD: 8,
  Space: 16,
};

function setJoy(bits: number): void {
  if (bits === state.joy) return;
  state.joy = bits;
  send({ type: "joy", bits });
  renderJoy();
}

window.addEventListener("keydown", (e) => {
  if ((e.target as HTMLElement).tagName === "INPUT") return;
  const bit = JOY_KEYS[e.code];
  if (bit !== undefined) {
    e.preventDefault();
    setJoy(state.joy | bit);
    return;
  }
  const mod = e.ctrlKey || e.metaKey;
  if (mod && e.key.toLowerCase() === "z") {
    e.preventDefault();
    send({ type: e.shiftKey ? "redo" : "undo" });
  } else if (mod && e.key.toLowerCase() === "g") {
    e.preventDefault();
    makeChip();
  } else if (mod && e.key.toLowerCase() === "c") {
    copySelection();
  } else if (mod && e.key.toLowerCase() === "v") {
    e.preventDefault();
    paste();
  } else if (mod && e.key.toLowerCase() === "y") {
    e.preventDefault();
    send({ type: "redo" });
  } else if (e.key === "Delete" || e.key === "Backspace") {
    deleteSelection();
  } else if (e.key === "Escape") {
    goTo(state.path.length - 1);
  }
});

window.addEventListener("keyup", (e) => {
  const bit = JOY_KEYS[e.code];
  if (bit !== undefined) setJoy(state.joy & ~bit);
});
window.addEventListener("blur", () => setJoy(0));

/**
 * makeChip turns the selected parts into a new chip definition and replaces
 * them with one instance. Every wire crossing the selection boundary becomes
 * an interface pin: one In per outside driver, one Out per inside driver.
 */
function makeChip(): void {
  const chip = currentChip();
  const d = state.design;
  if (!chip || !d) return;
  const inside = [...state.selected].filter((id) => chip.parts[id] && chip.parts[id]?.kind !== "in" && chip.parts[id]?.kind !== "out");
  if (inside.length === 0) {
    toast("Select some parts first (shift-drag to select)");
    return;
  }
  const name = window.prompt("Name for the new chip", "MyChip")?.trim();
  if (!name) return;
  const sel = new Set(inside);
  const dirOf = (ref: { part: string; pin: string }): number => {
    const p = chip.parts[ref.part] as Part;
    return shapeOf(d, p).pins.find((q) => q.name === ref.pin)?.dir ?? 0;
  };
  const widthOf = (ref: { part: string; pin: string }): number => {
    const p = chip.parts[ref.part] as Part;
    return shapeOf(d, p).pins.find((q) => q.name === ref.pin)?.width ?? 1;
  };
  let x0 = Infinity;
  let y0 = Infinity;
  let x1 = -Infinity;
  for (const id of inside) {
    const p = chip.parts[id] as Part;
    x0 = Math.min(x0, p.x);
    y0 = Math.min(y0, p.y);
    x1 = Math.max(x1, p.x + shapeOf(d, p).w);
  }
  const def: ChipDef = { id: newId("chip"), name, parts: {}, wires: {} };
  for (const id of inside) {
    const p = structuredClone(chip.parts[id] as Part);
    p.x -= x0 - 8;
    p.y -= y0;
    def.parts[id] = p;
  }
  const pinFor = new Map<string, string>(); // driver pin key -> interface part ID
  const crossings: { outer: { part: string; pin: string }; pin: string }[] = [];
  let ins = 0;
  let outs = 0;
  for (const w of Object.values(chip.wires)) {
    const a = sel.has(w.from.part);
    const b = sel.has(w.to.part);
    if (a && b) {
      def.wires[w.id] = { id: w.id, from: w.from, to: w.to };
      continue;
    }
    if (!a && !b) continue;
    // Orient the crossing so `inner` is the selected end.
    const inner = a ? w.from : w.to;
    const outer = a ? w.to : w.from;
    const drivesOut = dirOf(inner) === 1;
    const key = drivesOut ? `o:${inner.part}.${inner.pin}` : `i:${outer.part}.${outer.pin}`;
    let pid = pinFor.get(key);
    if (!pid) {
      pid = drivesOut ? `out${++outs}` : `in${++ins}`;
      pinFor.set(key, pid);
      const width = widthOf(inner);
      def.parts[pid] = drivesOut
        ? { id: pid, kind: "out", label: pid, width, x: x1 - x0 + 12, y: 2 * outs }
        : { id: pid, kind: "in", label: pid, width, x: 0, y: 2 * ins };
      if (drivesOut) def.wires[`w_${pid}`] = { id: `w_${pid}`, from: inner, to: { part: pid, pin: "a" } };
    }
    if (!drivesOut) {
      // An input pin may feed several inside pins: one inner wire each.
      const wid = `w_${pid}_${inner.part}_${inner.pin}`;
      def.wires[wid] = { id: wid, from: { part: pid, pin: "q" }, to: inner };
    }
    crossings.push({ outer, pin: pid });
  }
  const inst: Part = { id: newId("u"), kind: "chip", chip: def.id, label: name, x: x0, y: y0 };
  const ops: Op[] = [{ t: "addChip", def }];
  for (const id of inside) ops.push({ t: "removePart", chip: chip.id, id });
  ops.push({ t: "addPart", chip: chip.id, part: inst });
  const seen = new Set<string>();
  for (const { outer, pin } of crossings) {
    const key = `${outer.part}.${outer.pin}>${pin}`;
    if (seen.has(key)) continue;
    seen.add(key);
    const instPin = { part: inst.id, pin };
    const fromOut = def.parts[pin]?.kind === "out";
    ops.push({
      t: "addWire",
      chip: chip.id,
      wire: { id: newId("w"), from: fromOut ? instPin : outer, to: fromOut ? outer : instPin },
    });
  }
  edit({ t: "batch", ops });
  state.selected = new Set([inst.id]);
  renderPalette();
}

// The clipboard holds parts and the wires among them, re-keyed on paste.
let clipboard: { parts: Part[]; wires: Wire[] } | null = null;

function copySelection(): void {
  const chip = currentChip();
  if (!chip) return;
  const parts = [...state.selected].flatMap((id) => (chip.parts[id] ? [structuredClone(chip.parts[id] as Part)] : []));
  const ids = new Set(parts.map((p) => p.id));
  const wires = Object.values(chip.wires).filter((w) => ids.has(w.from.part) && ids.has(w.to.part));
  if (parts.length > 0) {
    clipboard = { parts, wires: structuredClone(wires) };
    toast(`Copied ${parts.length} part${parts.length === 1 ? "" : "s"}`);
  }
}

function paste(): void {
  const chip = currentChip();
  if (!chip || !clipboard) return;
  const rename = new Map<string, string>();
  const ops: Op[] = [];
  for (const p of clipboard.parts) {
    const id = newId("p");
    rename.set(p.id, id);
    ops.push({ t: "addPart", chip: chip.id, part: { ...p, id, x: p.x + 3, y: p.y + 3 } });
  }
  for (const w of clipboard.wires) {
    const from = { part: rename.get(w.from.part) ?? "", pin: w.from.pin };
    const to = { part: rename.get(w.to.part) ?? "", pin: w.to.pin };
    ops.push({ t: "addWire", chip: chip.id, wire: { id: newId("w"), from, to } });
  }
  if (ops.length === 0) return;
  edit({ t: "batch", ops });
  state.selected = new Set(rename.values());
  clipboard = { parts: clipboard.parts.map((p) => ({ ...p, x: p.x + 3, y: p.y + 3 })), wires: clipboard.wires };
}

function deleteSelection(): void {
  const chip = currentChip();
  if (!chip || state.selected.size === 0) return;
  const ops: Op[] = [];
  for (const id of state.selected) {
    if (chip.parts[id]) ops.push({ t: "removePart", chip: chip.id, id });
    else if (chip.wires[id]) ops.push({ t: "removeWire", chip: chip.id, id });
  }
  state.selected.clear();
  if (ops.length > 0) edit(ops.length === 1 ? (ops[0] as Op) : { t: "batch", ops });
}

let cursorPos: Point = { x: 0, y: 0 };
setInterval(() => {
  send({ type: "cursor", x: cursorPos.x, y: cursorPos.y, path: state.path });
}, 80);

// ---- chrome ---------------------------------------------------------------

const PRIMITIVES: { kind: string; label: string; part: Partial<Part> }[] = [
  { kind: "nand", label: "NAND", part: {} },
  { kind: "dff", label: "DFF", part: {} },
  { kind: "switch", label: "Switch", part: {} },
  { kind: "button", label: "Button", part: { label: "btn" } },
  { kind: "clock", label: "Clock", part: {} },
  { kind: "led", label: "LED", part: {} },
  { kind: "seg7", label: "7-seg", part: {} },
  { kind: "const", label: "Const 1", part: { width: 1, value: 1 } },
  { kind: "in", label: "Pin in", part: { width: 1, label: "in" } },
  { kind: "out", label: "Pin out", part: { width: 1, label: "out" } },
];

function addPart(base: Partial<Part>): void {
  const chip = currentChip();
  if (!chip) return;
  const c = state.cam;
  const d = state.design as Design;
  const part = { id: newId("p"), x: 0, y: 0, ...base } as Part;
  const s = shapeOf(d, part);
  part.x = Math.round(c.x + canvas.clientWidth / c.scale / 2 - s.w / 2);
  part.y = Math.round(c.y + canvas.clientHeight / c.scale / 2 - s.h / 2);
  // Drop it at the view centre, nudged down until it overlaps nothing.
  const others = Object.values(chip.parts).map((p) => ({ p, s: shapeOf(d, p) }));
  for (let tries = 0; tries < 60; tries++) {
    const hit = others.some(
      ({ p, s: o }) => part.x < p.x + o.w + 2 && p.x < part.x + s.w + 2 && part.y < p.y + o.h + 1 && p.y < part.y + s.h + 1,
    );
    if (!hit) break;
    part.y += 2;
  }
  edit({ t: "addPart", chip: chip.id, part });
  state.selected = new Set([part.id]);
}

function renderPalette(): void {
  const el = $("palette");
  el.replaceChildren();
  const make = document.createElement("button");
  make.textContent = "Make chip (Ctrl+G)";
  make.className = "accent";
  make.title = "Turn the selected parts into a new chip";
  make.onclick = () => makeChip();
  el.append(make);
  const head = document.createElement("h3");
  head.textContent = "Parts";
  el.append(head);
  for (const prim of PRIMITIVES) {
    const b = document.createElement("button");
    b.textContent = prim.label;
    b.onclick = () => addPart({ kind: prim.kind, ...prim.part });
    el.append(b);
  }
  const d = state.design;
  if (!d) return;
  const head2 = document.createElement("h3");
  head2.textContent = "Chips";
  el.append(head2);
  for (const c of Object.values(d.chips).sort((a, b) => a.name.localeCompare(b.name))) {
    if (c.id === d.root) continue;
    const b = document.createElement("button");
    b.textContent = c.name;
    b.className = "chip";
    b.onclick = () => addPart({ kind: "chip", chip: c.id });
    el.append(b);
  }
}

function renderCrumbs(): void {
  const el = $("crumbs");
  el.replaceChildren();
  const d = state.design;
  if (!d) return;
  let chip = d.chips[d.root];
  const items: { label: string; depth: number }[] = [{ label: chip?.name ?? "World", depth: 0 }];
  state.path.forEach((id, i) => {
    const p = chip?.parts[id];
    const inner = p?.chip ? d.chips[p.chip] : undefined;
    items.push({ label: `${inner?.name ?? "?"}${p?.label && p.label !== inner?.name ? ` · ${p.label}` : ""}`, depth: i + 1 });
    chip = inner;
  });
  items.forEach((it, i) => {
    const a = document.createElement("button");
    a.textContent = it.label;
    a.className = i === items.length - 1 ? "crumb current" : "crumb";
    a.onclick = () => goTo(it.depth);
    el.append(a);
    if (i < items.length - 1) el.append(Object.assign(document.createElement("span"), { textContent: "›", className: "sep" }));
  });
}

function renderPeers(): void {
  const el = $("peers");
  el.replaceChildren();
  for (const p of [state.me, ...state.peers]) {
    if (!p) continue;
    const dot = document.createElement("span");
    dot.className = "peer";
    dot.style.background = p.color;
    dot.title = p === state.me ? `${p.name} (you)` : p.name;
    dot.textContent = p.name.slice(0, 1).toUpperCase();
    if (p === state.me) {
      dot.classList.add("me");
      dot.title += " - click to rename";
      dot.onclick = () => rename();
    }
    el.append(dot);
  }
  state.dirty = true;
}

function rename(): void {
  const me = state.me;
  const name = window.prompt("Your name in this room", me?.name ?? "")?.trim();
  if (!me || !name) return;
  me.name = name.slice(0, 24);
  localStorage.setItem("nandbox-name", me.name);
  send({ type: "name", name: me.name });
  renderPeers();
}

function fmtHz(hz: number): string {
  return hz >= 1e6 ? `${(hz / 1e6).toFixed(2)} MHz` : hz >= 1e3 ? `${(hz / 1e3).toFixed(1)} kHz` : `${hz.toFixed(0)} Hz`;
}

function renderSim(): void {
  const s = state.sim;
  if (!s) return;
  $("run").textContent = s.running ? "❚❚ Pause" : "▶ Run";
  $("hz").textContent = `${fmtHz(s.actualHz)} / ${fmtHz(s.hz)}`;
  ($("speed") as HTMLInputElement).value = String(Math.log10(s.hz));
  $("stats").textContent = `${s.stats.nands.toLocaleString()} NAND · ${s.stats.dffs} DFF · ${s.cycles.toLocaleString()} cycles`;
  const err = $("error");
  err.textContent = s.error ? `⚠ ${s.error.msg}` : "";
  err.hidden = !s.error;
}

function renderDock(): void {
  const px = state.matrix;
  const n = 16;
  const cell = screen.width / n;
  sctx.fillStyle = "#05070c";
  sctx.fillRect(0, 0, screen.width, screen.height);
  for (let i = 0; i < 256; i++) {
    const v = px ? (px[i] ?? 0) & 15 : 0;
    const color = MATRIX_PALETTE[v] ?? "#000";
    sctx.fillStyle = color;
    if (v !== 0) {
      sctx.shadowColor = color;
      sctx.shadowBlur = 8;
    }
    sctx.beginPath();
    sctx.arc((i % n) * cell + cell / 2, Math.floor(i / n) * cell + cell / 2, cell * 0.38, 0, Math.PI * 2);
    sctx.fill();
    sctx.shadowBlur = 0;
  }
  const seg = $<HTMLCanvasElement>("score");
  const g = seg.getContext("2d") as CanvasRenderingContext2D;
  g.setTransform(1, 0, 0, 1, 0, 0);
  g.fillStyle = "#0e0a0c";
  g.fillRect(0, 0, seg.width, seg.height);
  state.segs.forEach((v, i) => {
    segmentDigit(g, 8 + i * 34, 8, 24, 40, (name) => ((v >> "abcdefg".indexOf(name)) & 1) === 1);
  });
  const leds = $("leds");
  leds.replaceChildren();
  for (let i = 0; i < 8; i++) {
    const d = document.createElement("span");
    d.className = (state.leds >> i) & 1 ? "led on" : "led";
    leds.append(d);
  }
  $("ir").textContent = state.pc >= 0 ? `PC ${state.pc.toString(16).padStart(2, "0")}  ${disassemble(state.ir)}` : "";
}

function renderJoy(): void {
  for (const [id, bit] of [
    ["j-up", 1],
    ["j-down", 2],
    ["j-left", 4],
    ["j-right", 8],
  ] as const) {
    $(id).classList.toggle("on", (state.joy & bit) !== 0);
  }
}

let toastTimer = 0;
function toast(msg: string): void {
  const el = $("toast");
  el.textContent = msg;
  el.hidden = false;
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => {
    el.hidden = true;
  }, 3500);
}

function setStatus(s: string): void {
  $("status").textContent = s;
}

$("run").onclick = () => send({ type: "sim", action: state.sim?.running ? "pause" : "run" });
$("step").onclick = () => send({ type: "sim", action: "step" });
$("undo").onclick = () => send({ type: "undo" });
$("redo").onclick = () => send({ type: "redo" });
$("fit").onclick = () => {
  const chip = currentChip();
  if (chip) fitView(chip, true);
};
($("speed") as HTMLInputElement).oninput = (e) => {
  send({ type: "sim", action: "speed", hz: Math.round(10 ** Number((e.target as HTMLInputElement).value)) });
};
$("share").onclick = () => {
  void navigator.clipboard?.writeText(location.href);
  toast("Room link copied - anyone with it joins this world");
};
for (const [id, bit] of [
  ["j-up", 1],
  ["j-down", 2],
  ["j-left", 4],
  ["j-right", 8],
] as const) {
  const el = $(id);
  el.onpointerdown = () => setJoy(state.joy | bit);
  el.onpointerup = el.onpointerleave = () => setJoy(state.joy & ~bit);
}

// ---- frame loop -------------------------------------------------------------

function resize(): void {
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.round(canvas.clientWidth * dpr);
  canvas.height = Math.round(canvas.clientHeight * dpr);
  state.dirty = true;
  sendView();
}
window.addEventListener("resize", resize);

function errorParts(): Set<string> {
  const out = new Set<string>();
  for (const l of state.sim?.error?.locations ?? []) {
    if (l.path.join("/") === state.path.join("/")) out.add(l.part);
  }
  return out;
}

function frame(now: number): void {
  if (anim) {
    const t = Math.min(1, (now - anim.t0) / anim.ms);
    const e = t < 0.5 ? 4 * t * t * t : 1 - (-2 * t + 2) ** 3 / 2;
    const lerp = (a: number, b: number): number => a + (b - a) * e;
    const ls = Math.exp(lerp(Math.log(anim.from.scale), Math.log(anim.to.scale)));
    // Interpolate the view centre so zooms feel anchored.
    const cw = canvas.clientWidth;
    const ch = canvas.clientHeight;
    const cx = lerp(anim.from.x + cw / anim.from.scale / 2, anim.to.x + cw / anim.to.scale / 2);
    const cy = lerp(anim.from.y + ch / anim.from.scale / 2, anim.to.y + ch / anim.to.scale / 2);
    state.cam = { scale: ls, x: cx - cw / ls / 2, y: cy - ch / ls / 2 };
    state.dirty = true;
    if (t >= 1) {
      const done = anim.done;
      anim = null;
      sendView();
      done?.();
    }
  }
  const chip = currentChip();
  if (chip && state.design && state.dirty) {
    state.dirty = false;
    const scene: Scene = {
      design: state.design,
      chip,
      signals: {
        wire: (id) => state.wireVals.get(id),
        pin: (part, pin) => state.pinVals.get(`${part}.${pin}`),
      },
      selected: state.selected,
      errorParts: errorParts(),
      hover,
      drag: gesture?.kind === "wire" ? { from: gesture.from.pos, to: gesture.to, ok: gesture.ok } : null,
      marquee: gesture?.kind === "marquee" ? { a: gesture.start, b: gesture.to } : null,
      peers: state.peers,
      path: state.path,
      matrix: state.path.length === 0 ? state.matrix : null,
    };
    draw(ctx, state.cam, canvas.clientWidth, canvas.clientHeight, scene);
  }
  requestAnimationFrame(frame);
}

resize();
renderDock();
requestAnimationFrame(frame);
void connect().catch((e: unknown) => setStatus(`Could not open a room: ${String(e)}`));

// Automation hook used by the capture script and the browser smoke test
// (tools/capture): dive by label and read what the canvas shows.
const toScreen = (w: Point): Point => {
  const r = canvas.getBoundingClientRect();
  return { x: r.left + (w.x - state.cam.x) * state.cam.scale, y: r.top + (w.y - state.cam.y) * state.cam.scale };
};
(window as unknown as { nandbox: object }).nandbox = {
  parts: () => {
    const chip = currentChip();
    const d = state.design;
    if (!chip || !d) return [];
    return Object.values(chip.parts).map((p) => {
      const s = shapeOf(d, p);
      const pins: Record<string, Point> = {};
      for (const pin of s.pins) pins[pin.name] = toScreen({ x: p.x + pin.x, y: p.y + pin.y });
      const a = toScreen({ x: p.x, y: p.y });
      const b = toScreen({ x: p.x + s.w, y: p.y + s.h });
      return {
        id: p.id,
        kind: p.kind,
        pins,
        center: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 },
        box: { x0: a.x, y0: a.y, x1: b.x, y1: b.y },
      };
    });
  },
  wires: (): number => Object.keys(currentChip()?.wires ?? {}).length,
  pinValue: (kind: string, pin: string): number | undefined => {
    const part = Object.values(currentChip()?.parts ?? {}).find((p) => p.kind === kind);
    return part ? state.pinVals.get(`${part.id}.${pin}`) : undefined;
  },
  peerCursors: (): number => state.peers.filter((p) => p.path.join("/") === state.path.join("/")).length,
  dive: (label: string): boolean => {
    const chip = currentChip();
    const part = chip ? Object.values(chip.parts).find((p) => p.kind === "chip" && (p.label === label || p.id === label)) : undefined;
    if (part) dive(part);
    return part !== undefined;
  },
};
