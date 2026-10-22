#!/usr/bin/env node
// Prüft, dass überall dieselbe Go-Version steht: go.mod (toolchain), jede
// setup-go-Version in .github/workflows und das Build-Image im Dockerfile.
// Sonst wird ein Compiler ausgeliefert, gegen den CI nie getestet hat.
'use strict';
const fs = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '..');
const read = (p) => fs.readFileSync(path.join(root, p), 'utf8');
const found = [];

const tc = read('go.mod').match(/^toolchain\s+go(\d+\.\d+\.\d+)\s*$/m);
if (!tc) { console.error('go.mod: keine "toolchain goX.Y.Z"-Zeile gefunden'); process.exit(1); }
found.push(['go.mod toolchain', tc[1]]);

const wfDir = path.join(root, '.github', 'workflows');
for (const f of fs.readdirSync(wfDir).filter((n) => /\.ya?ml$/.test(n)).sort()) {
  const src = fs.readFileSync(path.join(wfDir, f), 'utf8');
  for (const m of src.matchAll(/^\s*(?:go-version|GO_VERSION):\s*["']?([^"'\s#]+)["']?/gm)) {
    if (m[1].startsWith('${{')) continue; // verweist auf GO_VERSION, das selbst geprüft wird
    found.push([`.github/workflows/${f}`, m[1]]);
  }
}

for (const file of ['Dockerfile', 'scripts/validation/Dockerfile']) {
  const img = read(file).match(/^FROM\s+golang:([^\s@]+?)(?:-alpine[^\s@]*)?(?:@\S+)?\s/m);
  if (!img) { console.error(`${file}: kein "FROM golang:…"-Build-Image gefunden`); process.exit(1); }
  found.push([file, img[1]]);
}

const want = tc[1];
const bad = found.filter(([, v]) => v !== want);
for (const [where, v] of found) console.log(`${v === want ? 'ok ' : 'ABW'}  ${v.padEnd(8)} ${where}`);
if (bad.length) {
  console.error(`\nGo-Version weicht ab: erwartet ${want} (go.mod toolchain) an ${bad.length} Stelle(n).`);
  process.exit(1);
}
console.log(`\nÜberall Go ${want}.`);
