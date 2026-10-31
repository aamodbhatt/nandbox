// Canvas renderer: parts, orthogonal wires coloured by live signal values,
// buses with hex values, devices and peer cursors. World units are grid units.
import { type ChipDef, type Design, MATRIX_SIDE, type Part, type Point, partName, SEGMENTS, type Shape, shapeOf } from "./model";

export interface Camera {
  x: number; // world coordinate at the left edge
  y: number;
  scale: number; // pixels per grid unit
}

export interface Signals {
  wire(id: string): number | undefined;
  pin(part: string, pin: string): number | undefined;
}

export interface Peer {
  id: string;
  name: string;
  color: string;
  x: number;
  y: number;
  path: string[];
}

export const COLORS = {
  bg: "#0b0f17",
  grid: "#161d2b",
  body: "#141b28",
  bodyEdge: "#2c3850",
  chip: "#121a2a",
  chipEdge: "#3b4d72",
  text: "#c9d4ea",
  dim: "#6b7a99",
  low: "#26324a",
  high: "#3dffa0",
  busLow: "#2d3d5c",
  busHigh: "#4cc9ff",
  unknown: "#3a4256",
  select: "#ffcc4d",
  error: "#ff5d6c",
};

// The LED matrix palette: 0 off, 1-4 snake body, 5 food, 6 head, 7 crash, 8-15 rainbow.
export const MATRIX_PALETTE = [
  "#121826",
  "#1fae5b",
  "#25c267",
  "#1fae5b",
  "#25c267",
  "#ff4d5e",
  "#6dffa8",
  "#ff9f1c",
  "#f4f4f4",
  "#ff5c8a",
  "#ff8a3d",
  "#ffd23f",
  "#5cff8f",
  "#3ddcff",
  "#5c8aff",
  "#c77dff",
];

export interface Scene {
  design: Design;
  chip: ChipDef;
  signals: Signals;
  selected: Set<string>;
  errorParts: Set<string>;
  hover: { part: string; pin: string } | null;
  drag: { from: Point; to: Point; ok: boolean } | null;
  marquee: { a: Point; b: Point } | null;
  peers: Peer[];
  path: string[];
  matrix: Uint8Array | null;
}

export function pinPos(p: Part, s: Shape, name: string): Point | null {
  const pin = s.pins.find((q) => q.name === name);
  return pin ? { x: p.x + pin.x, y: p.y + pin.y } : null;
}

/** autoRoute draws a clean orthogonal path when a wire has no stored route. */
export function autoRoute(a: Point, b: Point): Point[] {
  if (a.y === b.y) return [];
  if (b.x > a.x + 1) {
    const mx = Math.round((a.x + b.x) / 2);
    return [
      { x: mx, y: a.y },
      { x: mx, y: b.y },
    ];
  }
  const my = Math.round((a.y + b.y) / 2);
  return [
    { x: a.x + 1, y: a.y },
    { x: a.x + 1, y: my },
    { x: b.x - 1, y: my },
    { x: b.x - 1, y: b.y },
  ];
}

function orthogonal(pts: Point[]): boolean {
  for (let i = 1; i < pts.length; i++) {
    const p = pts[i - 1] as Point;
    const q = pts[i] as Point;
    if (p.x !== q.x && p.y !== q.y) return false;
  }
  return true;
}

export function wirePath(d: Design, c: ChipDef, wid: string): Point[] | null {
  const w = c.wires[wid];
  if (!w) return null;
  const pa = c.parts[w.from.part];
  const pb = c.parts[w.to.part];
  if (!pa || !pb) return null;
  const a = pinPos(pa, shapeOf(d, pa), w.from.pin);
  const b = pinPos(pb, shapeOf(d, pb), w.to.pin);
  if (!a || !b) return null;
  if (w.route && w.route.length > 0) {
    const full = [a, ...w.route, b];
    if (orthogonal(full)) return full;
  }
  return [a, ...autoRoute(a, b), b];
}

export function draw(ctx: CanvasRenderingContext2D, cam: Camera, w: number, h: number, sc: Scene): void {
  const dpr = window.devicePixelRatio || 1;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.fillStyle = COLORS.bg;
  ctx.fillRect(0, 0, w, h);
  ctx.setTransform(cam.scale * dpr, 0, 0, cam.scale * dpr, -cam.x * cam.scale * dpr, -cam.y * cam.scale * dpr);
  const view = { x0: cam.x, y0: cam.y, x1: cam.x + w / cam.scale, y1: cam.y + h / cam.scale };
  drawGrid(ctx, cam, view);
  const detail = cam.scale >= 7;
  drawWires(ctx, sc, view, cam);
  for (const p of Object.values(sc.chip.parts)) {
    const s = shapeOf(sc.design, p);
    if (p.x + s.w < view.x0 - 2 || p.x > view.x1 + 2 || p.y + s.h < view.y0 - 2 || p.y > view.y1 + 2) continue;
    drawPart(ctx, sc, p, s, detail);
  }
  if (sc.drag) {
    ctx.strokeStyle = sc.drag.ok ? COLORS.select : COLORS.error;
    ctx.lineWidth = 0.15;
    ctx.setLineDash([0.4, 0.3]);
    const pts = [sc.drag.from, ...autoRoute(sc.drag.from, sc.drag.to), sc.drag.to];
    poly(ctx, pts);
    ctx.stroke();
    ctx.setLineDash([]);
  }
  if (sc.marquee) {
    const { a, b } = sc.marquee;
    ctx.fillStyle = "rgba(76, 201, 255, 0.08)";
    ctx.strokeStyle = COLORS.busHigh;
    ctx.lineWidth = 1 / cam.scale;
    ctx.fillRect(Math.min(a.x, b.x), Math.min(a.y, b.y), Math.abs(b.x - a.x), Math.abs(b.y - a.y));
    ctx.strokeRect(Math.min(a.x, b.x), Math.min(a.y, b.y), Math.abs(b.x - a.x), Math.abs(b.y - a.y));
  }
  for (const peer of sc.peers) {
    if (peer.path.join("/") !== sc.path.join("/")) continue;
    drawCursor(ctx, peer, cam.scale);
  }
}

function drawGrid(ctx: CanvasRenderingContext2D, cam: Camera, v: { x0: number; y0: number; x1: number; y1: number }): void {
  const step = cam.scale < 5 ? 10 : 2;
  ctx.fillStyle = COLORS.grid;
  const r = 1.4 / cam.scale;
  for (let x = Math.floor(v.x0 / step) * step; x <= v.x1; x += step) {
    for (let y = Math.floor(v.y0 / step) * step; y <= v.y1; y += step) {
      ctx.fillRect(x - r, y - r, 2 * r, 2 * r);
    }
  }
}

function poly(ctx: CanvasRenderingContext2D, pts: Point[]): void {
  ctx.beginPath();
  pts.forEach((p, i) => {
    if (i === 0) ctx.moveTo(p.x, p.y);
    else ctx.lineTo(p.x, p.y);
  });
}

function drawWires(ctx: CanvasRenderingContext2D, sc: Scene, v: { x0: number; y0: number; x1: number; y1: number }, cam: Camera): void {
  const groups: Record<string, Point[][]> = { low: [], high: [], busLow: [], busHigh: [], unknown: [], sel: [] };
  const labels: { at: Point; text: string; hot: boolean }[] = [];
  for (const id of Object.keys(sc.chip.wires)) {
    const pts = wirePath(sc.design, sc.chip, id);
    if (!pts) continue;
    let minX = Infinity;
    let maxX = -Infinity;
    let minY = Infinity;
    let maxY = -Infinity;
    for (const p of pts) {
      minX = Math.min(minX, p.x);
      maxX = Math.max(maxX, p.x);
      minY = Math.min(minY, p.y);
      maxY = Math.max(maxY, p.y);
    }
    if (maxX < v.x0 || minX > v.x1 || maxY < v.y0 || minY > v.y1) continue;
    const wire = sc.chip.wires[id];
    const src = wire ? sc.chip.parts[wire.from.part] : undefined;
    const width = src ? (shapeOf(sc.design, src).pins.find((p) => p.name === wire?.from.pin)?.width ?? 1) : 1;
    const val = sc.signals.wire(id);
    let key = "unknown";
    if (sc.selected.has(id)) key = "sel";
    else if (val !== undefined) key = width > 1 ? (val !== 0 ? "busHigh" : "busLow") : val ? "high" : "low";
    groups[key]?.push(pts);
    if (width > 1 && val !== undefined && cam.scale >= 6) {
      // Label the longest horizontal segment with the bus value.
      let best = 0;
      let at: Point = pts[0] as Point;
      for (let i = 1; i < pts.length; i++) {
        const a = pts[i - 1] as Point;
        const b = pts[i] as Point;
        const len = Math.abs(b.x - a.x);
        if (a.y === b.y && len > best) {
          best = len;
          at = { x: (a.x + b.x) / 2, y: a.y };
        }
      }
      if (best >= 3)
        labels.push({
          at,
          text: val
            .toString(16)
            .toUpperCase()
            .padStart(Math.ceil(width / 4), "0"),
          hot: val !== 0,
        });
    }
  }
  const stroke = (key: string, color: string, lw: number, glow: boolean): void => {
    const list = groups[key];
    if (!list || list.length === 0) return;
    ctx.lineJoin = "round";
    ctx.lineCap = "round";
    if (glow) {
      ctx.strokeStyle = color;
      ctx.globalAlpha = 0.18;
      ctx.lineWidth = lw * 4;
      ctx.beginPath();
      for (const pts of list) addPoly(ctx, pts);
      ctx.stroke();
      ctx.globalAlpha = 1;
    }
    ctx.strokeStyle = color;
    ctx.lineWidth = lw;
    ctx.beginPath();
    for (const pts of list) addPoly(ctx, pts);
    ctx.stroke();
  };
  stroke("unknown", COLORS.unknown, 0.12, false);
  stroke("low", COLORS.low, 0.14, false);
  stroke("busLow", COLORS.busLow, 0.36, false);
  stroke("busHigh", COLORS.busHigh, 0.36, true);
  stroke("high", COLORS.high, 0.16, true);
  stroke("sel", COLORS.select, 0.22, false);
  ctx.font = "600 0.9px ui-monospace, Menlo, Consolas, monospace";
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  for (const l of labels) {
    const tw = ctx.measureText(l.text).width + 0.6;
    ctx.fillStyle = COLORS.bg;
    ctx.fillRect(l.at.x - tw / 2, l.at.y - 0.6, tw, 1.2);
    ctx.fillStyle = l.hot ? COLORS.busHigh : COLORS.dim;
    ctx.fillText(l.text, l.at.x, l.at.y + 0.05);
  }
}

function addPoly(ctx: CanvasRenderingContext2D, pts: Point[]): void {
  pts.forEach((p, i) => {
    if (i === 0) ctx.moveTo(p.x, p.y);
    else ctx.lineTo(p.x, p.y);
  });
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number): void {
  ctx.beginPath();
  ctx.roundRect(x, y, w, h, r);
}

function text(
  ctx: CanvasRenderingContext2D,
  s: string,
  x: number,
  y: number,
  size: number,
  color: string,
  align: CanvasTextAlign = "center",
): void {
  ctx.font = `500 ${size}px system-ui, -apple-system, Segoe UI, sans-serif`;
  ctx.fillStyle = color;
  ctx.textAlign = align;
  ctx.textBaseline = "middle";
  ctx.fillText(s, x, y);
}

function drawPart(ctx: CanvasRenderingContext2D, sc: Scene, p: Part, s: Shape, detail: boolean): void {
  const sel = sc.selected.has(p.id);
  const err = sc.errorParts.has(p.id);
  const edge = err ? COLORS.error : sel ? COLORS.select : COLORS.bodyEdge;
  const pv = (pin: string): number | undefined => sc.signals.pin(p.id, pin);
  ctx.lineWidth = sel || err ? 0.18 : 0.1;
  switch (p.kind) {
    case "nand": {
      const on = pv("y") === 1;
      ctx.beginPath();
      ctx.moveTo(p.x + 0.4, p.y + 0.3);
      ctx.lineTo(p.x + 1.8, p.y + 0.3);
      ctx.arc(p.x + 1.8, p.y + 2, 1.7, -Math.PI / 2, Math.PI / 2);
      ctx.lineTo(p.x + 0.4, p.y + 3.7);
      ctx.closePath();
      ctx.fillStyle = on ? "#16302a" : COLORS.body;
      ctx.fill();
      ctx.strokeStyle = on && !sel && !err ? "#2fd18a" : edge;
      ctx.stroke();
      ctx.beginPath();
      ctx.arc(p.x + 3.75, p.y + 2, 0.25, 0, Math.PI * 2);
      ctx.fillStyle = on ? COLORS.high : COLORS.low;
      ctx.fill();
      for (const y of [1, 3]) {
        ctx.beginPath();
        ctx.moveTo(p.x, p.y + y);
        ctx.lineTo(p.x + 0.4, p.y + y);
        ctx.strokeStyle = pv(y === 1 ? "a" : "b") === 1 ? COLORS.high : COLORS.low;
        ctx.stroke();
      }
      break;
    }
    case "splitter": {
      const top = s.pins.length > 1 ? 1 : 0;
      ctx.strokeStyle = COLORS.busLow;
      ctx.lineWidth = 0.36;
      const x = p.x + 1;
      ctx.beginPath();
      ctx.moveTo(x, p.y + top);
      ctx.lineTo(x, p.y + Math.max(top, s.h - 1));
      ctx.stroke();
      ctx.lineWidth = 0.12;
      for (const pin of s.pins) {
        ctx.beginPath();
        ctx.moveTo(p.x + pin.x, p.y + pin.y);
        ctx.lineTo(x, p.y + pin.y);
        ctx.stroke();
        if (detail && pin.label)
          text(ctx, pin.label, x + (p.flip ? -0.2 : 0.2), p.y + pin.y - 0.5, 0.6, COLORS.dim, p.flip ? "right" : "left");
      }
      break;
    }
    case "const":
      roundRect(ctx, p.x + 0.2, p.y + 0.3, 1.6, 1.4, 0.3);
      ctx.fillStyle = COLORS.body;
      ctx.fill();
      ctx.strokeStyle = edge;
      ctx.stroke();
      text(ctx, (p.value ?? 0).toString(16).toUpperCase(), p.x + 1, p.y + 1, 0.9, COLORS.dim);
      break;
    case "led": {
      const on = pv("a") === 1;
      ctx.beginPath();
      ctx.arc(p.x + 1, p.y + 1, 0.75, 0, Math.PI * 2);
      ctx.fillStyle = on ? "#ff5d6c" : "#2a1820";
      if (on) {
        ctx.shadowColor = "#ff5d6c";
        ctx.shadowBlur = 12;
      }
      ctx.fill();
      ctx.shadowBlur = 0;
      ctx.strokeStyle = edge;
      ctx.stroke();
      break;
    }
    case "seg7":
      drawSeg7(ctx, p, s, edge, (n) => pv(n) === 1);
      break;
    case "matrix":
      drawMatrix(ctx, p, s, edge, sc.matrix);
      break;
    default:
      drawBox(ctx, sc, p, s, edge, detail);
  }
  if (detail) {
    for (const pin of s.pins) {
      const v = pv(pin.name);
      ctx.beginPath();
      ctx.arc(p.x + pin.x, p.y + pin.y, pin.width > 1 ? 0.24 : 0.17, 0, Math.PI * 2);
      ctx.fillStyle =
        v === undefined ? COLORS.unknown : pin.width > 1 ? (v ? COLORS.busHigh : COLORS.busLow) : v ? COLORS.high : COLORS.low;
      ctx.fill();
    }
    const hv = sc.hover;
    if (hv && hv.part === p.id) {
      const pin = s.pins.find((q) => q.name === hv.pin);
      if (pin) {
        ctx.beginPath();
        ctx.arc(p.x + pin.x, p.y + pin.y, 0.45, 0, Math.PI * 2);
        ctx.strokeStyle = COLORS.select;
        ctx.lineWidth = 0.12;
        ctx.stroke();
      }
    }
  }
}

function drawBox(ctx: CanvasRenderingContext2D, sc: Scene, p: Part, s: Shape, edge: string, detail: boolean): void {
  const isChip = p.kind === "chip";
  roundRect(ctx, p.x, p.y, s.w, s.h, isChip ? 0.5 : 0.35);
  ctx.fillStyle = isChip ? COLORS.chip : COLORS.body;
  ctx.fill();
  ctx.strokeStyle = isChip && edge === COLORS.bodyEdge ? COLORS.chipEdge : edge;
  ctx.stroke();
  const pv = (pin: string): number | undefined => sc.signals.pin(p.id, pin);
  switch (p.kind) {
    case "chip": {
      const def = p.chip ? sc.design.chips[p.chip] : undefined;
      const title = def ? def.name : "?";
      const lab = p.label && p.label !== title ? p.label : "";
      // Big blocks get big titles so a block diagram reads from afar.
      const size = Math.min(2.2, Math.max(0.8, Math.min(s.w / 7, s.h / 4)));
      text(ctx, title, p.x + s.w / 2, p.y + s.h / 2 - (lab ? size * 0.4 : 0), size, "#e6edff");
      if (lab) text(ctx, lab, p.x + s.w / 2, p.y + s.h / 2 + size * 0.55, size * 0.6, COLORS.dim);
      if (detail) {
        for (const pin of s.pins) {
          text(
            ctx,
            pin.label,
            p.x + pin.x + (pin.dir === 0 ? 0.45 : -0.45),
            p.y + pin.y,
            0.6,
            COLORS.dim,
            pin.dir === 0 ? "left" : "right",
          );
        }
      }
      return;
    }
    case "in":
    case "out":
      text(ctx, partName(p), p.x + s.w / 2, p.y + 1, 0.75, COLORS.text);
      return;
    case "dff":
      text(ctx, "D", p.x + 0.5, p.y + 1, 0.7, COLORS.dim, "left");
      text(ctx, "Q", p.x + s.w - 0.5, p.y + 1, 0.7, COLORS.dim, "right");
      ctx.beginPath();
      ctx.moveTo(p.x, p.y + 2.5);
      ctx.lineTo(p.x + 0.6, p.y + 3);
      ctx.lineTo(p.x, p.y + 3.5);
      ctx.strokeStyle = COLORS.dim;
      ctx.stroke();
      return;
    case "clock":
      text(ctx, pv("q") === 1 ? "CLK ▲" : "CLK ▽", p.x + 2, p.y + 1, 0.7, pv("q") === 1 ? COLORS.high : COLORS.text);
      return;
    case "switch":
    case "button": {
      const on = pv("q") === 1;
      roundRect(ctx, p.x + 0.5, p.y + 0.5, 2.4, 1, 0.5);
      ctx.fillStyle = on ? "#1d6b4a" : "#1c2436";
      ctx.fill();
      text(ctx, p.kind === "button" ? partName(p) : on ? "ON" : "OFF", p.x + 1.7, p.y + 1, 0.55, on ? COLORS.high : COLORS.dim);
      return;
    }
    case "joystick":
      text(ctx, "🕹", p.x + 3, p.y + 5, 2.4, COLORS.text);
      for (const pin of s.pins)
        text(ctx, pin.name, p.x + s.w - 0.4, p.y + pin.y, 0.55, pv(pin.name) === 1 ? COLORS.high : COLORS.dim, "right");
      return;
    case "ram":
    case "rom":
      text(ctx, partName(p), p.x + s.w / 2, p.y + s.h / 2 + 0.4, 0.8, COLORS.text);
      if (detail) {
        for (const pin of s.pins) {
          text(ctx, pin.name, p.x + pin.x + (pin.dir === 0 ? 0.4 : -0.4), p.y + pin.y, 0.55, COLORS.dim, pin.dir === 0 ? "left" : "right");
        }
      }
      return;
    case "note":
      text(ctx, p.label ?? "", p.x, p.y + 1, 0.9, COLORS.text, "left");
      return;
  }
}

function drawSeg7(ctx: CanvasRenderingContext2D, p: Part, s: Shape, edge: string, lit: (n: string) => boolean): void {
  roundRect(ctx, p.x, p.y, s.w, s.h, 0.4);
  ctx.fillStyle = "#0e0a0c";
  ctx.fill();
  ctx.strokeStyle = edge;
  ctx.lineWidth = 0.1;
  ctx.stroke();
  segmentDigit(ctx, p.x + 2, p.y + 2.5, 4, 9, lit);
}

/** segmentDigit draws a seven-segment digit in the box (x, y, w, h). */
export function segmentDigit(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, lit: (n: string) => boolean): void {
  const t = w * 0.18;
  const half = h / 2;
  const segs: Record<string, [number, number, number, number]> = {
    a: [x + t, y, w - 2 * t, t],
    b: [x + w - t, y + t, t, half - 1.5 * t],
    c: [x + w - t, y + half + t / 2, t, half - 1.5 * t],
    d: [x + t, y + h - t, w - 2 * t, t],
    e: [x, y + half + t / 2, t, half - 1.5 * t],
    f: [x, y + t, t, half - 1.5 * t],
    g: [x + t, y + half - t / 2, w - 2 * t, t],
  };
  for (const n of SEGMENTS) {
    const r = segs[n];
    if (!r) continue;
    const on = lit(n);
    ctx.fillStyle = on ? "#ff3b4e" : "#2a1216";
    if (on) {
      ctx.shadowColor = "#ff3b4e";
      ctx.shadowBlur = 10;
    }
    ctx.beginPath();
    ctx.roundRect(r[0], r[1], r[2], r[3], t / 2);
    ctx.fill();
    ctx.shadowBlur = 0;
  }
}

function drawMatrix(ctx: CanvasRenderingContext2D, p: Part, s: Shape, edge: string, px: Uint8Array | null): void {
  roundRect(ctx, p.x, p.y, s.w, s.h, 0.8);
  ctx.fillStyle = "#070a10";
  ctx.fill();
  ctx.strokeStyle = edge;
  ctx.lineWidth = 0.12;
  ctx.stroke();
  for (let i = 0; i < MATRIX_SIDE * MATRIX_SIDE; i++) {
    const v = px ? (px[i] ?? 0) & 15 : 0;
    const cx = p.x + 2 + (i % MATRIX_SIDE) * 2 + 1;
    const cy = p.y + 2 + Math.floor(i / MATRIX_SIDE) * 2 + 1;
    ctx.beginPath();
    ctx.arc(cx, cy, 0.8, 0, Math.PI * 2);
    ctx.fillStyle = MATRIX_PALETTE[v] ?? "#000";
    ctx.fill();
  }
}

function drawCursor(ctx: CanvasRenderingContext2D, peer: Peer, scale: number): void {
  const k = 14 / scale;
  ctx.save();
  ctx.translate(peer.x, peer.y);
  ctx.scale(k, k);
  ctx.beginPath();
  ctx.moveTo(0, 0);
  ctx.lineTo(0, 1.2);
  ctx.lineTo(0.35, 0.9);
  ctx.lineTo(0.6, 1.4);
  ctx.lineTo(0.8, 1.3);
  ctx.lineTo(0.55, 0.8);
  ctx.lineTo(1, 0.8);
  ctx.closePath();
  ctx.fillStyle = peer.color;
  ctx.fill();
  ctx.font = "600 0.6px system-ui, sans-serif";
  const tw = ctx.measureText(peer.name).width;
  ctx.fillRect(0.9, 1.2, tw + 0.4, 0.8);
  ctx.fillStyle = "#0b0f17";
  ctx.textBaseline = "middle";
  ctx.textAlign = "left";
  ctx.fillText(peer.name, 1.1, 1.62);
  ctx.restore();
}
