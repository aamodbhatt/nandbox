// Document model and geometry. This mirrors internal/circuit (types and
// shape.go) and internal/server/ops.go so edits apply locally before the
// server confirms them.

export interface Point {
  x: number;
  y: number;
}
export interface Tap {
  lo: number;
  hi: number;
}
export interface Part {
  id: string;
  kind: string;
  x: number;
  y: number;
  label?: string;
  chip?: string;
  width?: number;
  value?: number;
  flip?: boolean;
  taps?: Tap[];
  addrBits?: number;
  dataBits?: number;
  words?: number[];
}
export interface PinRef {
  part: string;
  pin: string;
}
export interface Wire {
  id: string;
  from: PinRef;
  to: PinRef;
  route?: Point[];
}
export interface ChipDef {
  id: string;
  name: string;
  parts: Record<string, Part>;
  wires: Record<string, Wire>;
}
export interface Design {
  root: string;
  chips: Record<string, ChipDef>;
}
export interface Op {
  t: string;
  chip?: string;
  id?: string;
  part?: Part;
  wire?: Wire;
  x?: number;
  y?: number;
  label?: string;
  route?: Point[];
  ops?: Op[];
  def?: ChipDef;
}

export interface PinShape {
  name: string;
  label: string;
  dir: 0 | 1; // 0 = input (left), 1 = output (right)
  width: number;
  x: number;
  y: number;
}
export interface Shape {
  w: number;
  h: number;
  pins: PinShape[];
}

export const SEGMENTS = ["a", "b", "c", "d", "e", "f", "g"];
export const JOYSTICK = ["up", "down", "left", "right", "fire"];
export const MATRIX_SIDE = 16;

const pin = (name: string, dir: 0 | 1, width: number, x: number, y: number): PinShape => ({
  name,
  label: name,
  dir,
  width,
  x,
  y,
});

const runes = (s: string): number => [...s].length;

export function partName(p: Part): string {
  return p.label && p.label !== "" ? p.label : p.id;
}

export function effectiveTaps(p: Part): Tap[] {
  if (p.taps && p.taps.length > 0) return p.taps;
  const out: Tap[] = [];
  for (let i = 0; i < (p.width ?? 0); i++) out.push({ lo: i, hi: i + 1 });
  return out;
}

export function tapLabel(t: Tap): string {
  return t.hi - t.lo === 1 ? `${t.lo}` : `${t.hi - 1}:${t.lo}`;
}

/** Interface returns a chip's In and Out parts in pin order. */
export function chipInterface(c: ChipDef): { ins: Part[]; outs: Part[] } {
  const all = Object.values(c.parts).sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  const byPos = (a: Part, b: Part): number => a.y - b.y || a.x - b.x || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  return {
    ins: all.filter((p) => p.kind === "in").sort(byPos),
    outs: all.filter((p) => p.kind === "out").sort(byPos),
  };
}

export function chipShape(c: ChipDef): Shape {
  const { ins, outs } = chipInterface(c);
  let maxIn = 0;
  let maxOut = 0;
  for (const p of ins) maxIn = Math.max(maxIn, runes(partName(p)));
  for (const p of outs) maxOut = Math.max(maxOut, runes(partName(p)));
  let w = Math.max(6, Math.trunc((6 * (maxIn + maxOut) + 8 * runes(c.name) + 39) / 10));
  if (w % 2 === 1) w++;
  const h = Math.max(4, 2 * Math.max(ins.length, outs.length));
  const pins: PinShape[] = [];
  let off = Math.trunc((h - 2 * ins.length) / 2);
  ins.forEach((p, i) => {
    pins.push({ name: p.id, label: partName(p), dir: 0, width: p.width ?? 1, x: 0, y: off + 2 * i + 1 });
  });
  off = Math.trunc((h - 2 * outs.length) / 2);
  outs.forEach((p, i) => {
    pins.push({ name: p.id, label: partName(p), dir: 1, width: p.width ?? 1, x: w, y: off + 2 * i + 1 });
  });
  return { w, h, pins };
}

export function shapeOf(d: Design, p: Part): Shape {
  const w = p.width ?? 1;
  switch (p.kind) {
    case "nand":
      return { w: 4, h: 4, pins: [pin("a", 0, 1, 0, 1), pin("b", 0, 1, 0, 3), pin("y", 1, 1, 4, 2)] };
    case "dff":
      return { w: 4, h: 4, pins: [pin("d", 0, 1, 0, 1), pin("clk", 0, 1, 0, 3), pin("q", 1, 1, 4, 1)] };
    case "const":
      return { w: 2, h: 2, pins: [pin("q", 1, w, 2, 1)] };
    case "in":
      return { w: 4, h: 2, pins: [pin("q", 1, w, 4, 1)] };
    case "out":
      return { w: 4, h: 2, pins: [pin("a", 0, w, 0, 1)] };
    case "clock":
    case "switch":
    case "button":
      return { w: 4, h: 2, pins: [pin("q", 1, 1, 4, 1)] };
    case "joystick":
      return { w: 8, h: 10, pins: JOYSTICK.map((n, i) => pin(n, 1, 1, 8, 2 * i + 1)) };
    case "led":
      return { w: 2, h: 2, pins: [pin("a", 0, 1, 0, 1)] };
    case "seg7":
      return { w: 8, h: 14, pins: SEGMENTS.map((n, i) => pin(n, 0, 1, 0, 2 * i + 1)) };
    case "splitter": {
      const taps = effectiveTaps(p);
      const h = Math.max(2, 2 * taps.length);
      const flip = p.flip === true;
      const pins: PinShape[] = [{ name: "bus", label: "", dir: flip ? 1 : 0, width: w, x: flip ? 2 : 0, y: Math.trunc(h / 2) }];
      taps.forEach((t, i) => {
        pins.push({ name: `t${i}`, label: tapLabel(t), dir: flip ? 0 : 1, width: t.hi - t.lo, x: flip ? 0 : 2, y: 2 * i + 1 });
      });
      return { w: 2, h, pins };
    }
    case "ram":
      return {
        w: 10,
        h: 8,
        pins: [
          pin("addr", 0, p.addrBits ?? 1, 0, 1),
          pin("din", 0, p.dataBits ?? 1, 0, 3),
          pin("we", 0, 1, 0, 5),
          pin("clk", 0, 1, 0, 7),
          pin("dout", 1, p.dataBits ?? 1, 10, 1),
        ],
      };
    case "rom":
      return {
        w: 10,
        h: 6,
        pins: [
          pin("addr", 0, p.addrBits ?? 1, 0, 1),
          pin("en", 0, 1, 0, 3),
          pin("clk", 0, 1, 0, 5),
          pin("dout", 1, p.dataBits ?? 1, 10, 1),
        ],
      };
    case "matrix": {
      const n = 2 * MATRIX_SIDE + 4;
      return {
        w: n,
        h: n,
        pins: [pin("addr", 0, 8, 0, 1), pin("din", 0, 8, 0, 3), pin("we", 0, 1, 0, 5), pin("clk", 0, 1, 0, 7), pin("dout", 1, 8, n, 1)],
      };
    }
    case "note":
      return { w: Math.max(4, Math.trunc((6 * runes(p.label ?? "") + 29) / 10)), h: 2, pins: [] };
    case "chip": {
      const def = p.chip ? d.chips[p.chip] : undefined;
      return def ? chipShape(def) : { w: 4, h: 4, pins: [] };
    }
  }
  return { w: 2, h: 2, pins: [] };
}

function clone<T>(v: T): T {
  return structuredClone(v);
}

/** apply mutates d by op (as the server does) and throws on invalid ops. */
export function apply(d: Design, op: Op): void {
  if (op.t === "batch") {
    for (const sub of op.ops ?? []) apply(d, sub);
    return;
  }
  if (op.t === "addChip") {
    if (!op.def || d.chips[op.def.id]) throw new Error("bad addChip");
    d.chips[op.def.id] = clone(op.def);
    return;
  }
  if (op.t === "removeChip") {
    if (!d.chips[op.id ?? ""]) throw new Error("unknown chip");
    delete d.chips[op.id ?? ""];
    return;
  }
  const c = d.chips[op.chip ?? ""];
  if (!c) throw new Error(`unknown chip ${op.chip}`);
  switch (op.t) {
    case "addPart":
      if (!op.part || c.parts[op.part.id]) throw new Error("bad addPart");
      c.parts[op.part.id] = clone(op.part);
      return;
    case "removePart": {
      const id = op.id ?? "";
      if (!c.parts[id]) throw new Error("unknown part");
      for (const w of Object.values(c.wires)) {
        if (w.from.part === id || w.to.part === id) delete c.wires[w.id];
      }
      delete c.parts[id];
      return;
    }
    case "movePart": {
      const p = c.parts[op.id ?? ""];
      if (!p) throw new Error("unknown part");
      p.x = op.x ?? 0;
      p.y = op.y ?? 0;
      for (const w of Object.values(c.wires)) {
        if (w.from.part === p.id || w.to.part === p.id) delete w.route;
      }
      return;
    }
    case "setRoute": {
      const w = c.wires[op.id ?? ""];
      if (!w) throw new Error("unknown wire");
      w.route = op.route ? clone(op.route) : undefined;
      return;
    }
    case "addWire":
      if (!op.wire || c.wires[op.wire.id]) throw new Error("bad addWire");
      c.wires[op.wire.id] = clone(op.wire);
      return;
    case "removeWire":
      if (!c.wires[op.id ?? ""]) throw new Error("unknown wire");
      delete c.wires[op.id ?? ""];
      return;
    case "setLabel": {
      const p = c.parts[op.id ?? ""];
      if (!p) throw new Error("unknown part");
      p.label = op.label ?? "";
      return;
    }
  }
  throw new Error(`unknown op ${op.t}`);
}
