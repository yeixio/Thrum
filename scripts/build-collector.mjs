#!/usr/bin/env node
// Builds the Go collector as a Tauri sidecar binary.
//
// Tauri expects bundled binaries to be named with the Rust target triple,
// for example thrum-collector-aarch64-apple-darwin. This script works out
// the triple, maps it to GOOS and GOARCH, and builds into
// app/src-tauri/binaries/.
import { execFileSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

function hostTriple() {
  const out = execFileSync('rustc', ['-vV'], { encoding: 'utf8' });
  const line = out.split('\n').find((l) => l.startsWith('host:'));
  if (!line) throw new Error('could not read the host triple from rustc -vV');
  return line.slice('host:'.length).trim();
}

// Tauri sets TAURI_ENV_TARGET_TRIPLE when building for a specific target.
const triple = process.env.TAURI_ENV_TARGET_TRIPLE || hostTriple();

const arch = { x86_64: 'amd64', aarch64: 'arm64', i686: '386', armv7: 'arm' }[triple.split('-')[0]];
const os = triple.includes('apple') ? 'darwin' : triple.includes('windows') ? 'windows' : triple.includes('linux') ? 'linux' : null;
if (!arch || !os) throw new Error(`unsupported target triple: ${triple}`);

const outDir = join(root, 'app', 'src-tauri', 'binaries');
mkdirSync(outDir, { recursive: true });
const out = join(outDir, `thrum-collector-${triple}${os === 'windows' ? '.exe' : ''}`);

execFileSync('go', ['build', '-trimpath', '-ldflags=-s -w', '-o', out, './cmd/thrum-collector'], {
  cwd: join(root, 'collector'),
  stdio: 'inherit',
  env: { ...process.env, GOOS: os, GOARCH: arch, CGO_ENABLED: '0' },
});
console.log(`built ${out}`);
