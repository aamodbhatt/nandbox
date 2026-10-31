// Assembles captured PNG frames into the README GIF without native tools:
// box-filter downscale, one global palette, and inter-frame transparency
// (pixels equal to the previous frame become transparent) so the mostly
// static editor compresses to a small file.
//
//   node make-gif.mjs <frames-dir> <out.gif> [width=960] [fps=14]
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import gifenc from "gifenc";
import { PNG } from "pngjs";

const { GIFEncoder, quantize, applyPalette } = gifenc;
const [dir, out, widthArg = "960", fpsArg = "14"] = process.argv.slice(2);
if (!dir || !out) throw new Error("usage: node make-gif.mjs <frames-dir> <out.gif> [width] [fps]");
const files = readdirSync(dir).filter((f) => f.endsWith(".png")).sort();
const outW = Number(widthArg);
const delay = Math.round(1000 / Number(fpsArg));

function load(file) {
  const png = PNG.sync.read(readFileSync(join(dir, file)));
  const scale = png.width / outW;
  const outH = Math.round(png.height / scale);
  const rgba = new Uint8Array(outW * outH * 4);
  for (let y = 0; y < outH; y++) {
    const y0 = Math.floor(y * scale);
    const y1 = Math.max(y0 + 1, Math.floor((y + 1) * scale));
    for (let x = 0; x < outW; x++) {
      const x0 = Math.floor(x * scale);
      const x1 = Math.max(x0 + 1, Math.floor((x + 1) * scale));
      let r = 0, g = 0, b = 0, n = 0;
      for (let yy = y0; yy < y1; yy++) {
        for (let xx = x0; xx < x1; xx++) {
          const i = (yy * png.width + xx) * 4;
          r += png.data[i];
          g += png.data[i + 1];
          b += png.data[i + 2];
          n++;
        }
      }
      const o = (y * outW + x) * 4;
      rgba[o] = r / n;
      rgba[o + 1] = g / n;
      rgba[o + 2] = b / n;
      rgba[o + 3] = 255;
    }
  }
  return { rgba, height: outH };
}

// Global palette from a sample of frames (255 colours; index 255 = transparent).
const samples = [];
for (let i = 0; i < files.length; i += Math.max(1, Math.floor(files.length / 12))) samples.push(load(files[i]).rgba);
const joined = new Uint8Array(samples.reduce((n, s) => n + s.length, 0));
let off = 0;
for (const s of samples) {
  joined.set(s, off);
  off += s.length;
}
const palette = quantize(joined, 255);
while (palette.length < 256) palette.push([0, 0, 0]);
const TRANSPARENT = 255;

const gif = GIFEncoder();
let prev = null;
for (const [k, file] of files.entries()) {
  const { rgba, height } = load(file);
  const index = applyPalette(rgba, palette.slice(0, 255));
  const frame = new Uint8Array(index);
  if (prev) {
    for (let i = 0; i < frame.length; i++) if (frame[i] === prev[i]) frame[i] = TRANSPARENT;
  }
  gif.writeFrame(frame, outW, height, {
    palette,
    delay: k === files.length - 1 ? 1500 : delay,
    transparent: prev !== null,
    transparentIndex: TRANSPARENT,
    dispose: 1,
  });
  prev = index;
}
gif.finish();
writeFileSync(out, gif.bytes());
console.log(`${files.length} frames -> ${out} (${(gif.bytes().length / 1e6).toFixed(2)} MB)`);
