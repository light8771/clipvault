import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import * as theme from './theme-utils.js';
import * as hotkey from './hotkey-utils.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const surfaces = ['#FFFFFF', '#101820', '#102038', '#2B1C10', '#182230'];
const paletteSurfaces = [
  ['#FFFFFF', '#F0FDFB', '#E6F9F5'],
  ['#101820', '#070C14', '#0C1520'],
  ['#FFFFFF', '#F0F9FF', '#E0F2FE'],
  ['#102038', '#07111F', '#0B1B30'],
  ['#FFFFFF', '#FFF7ED', '#FFEDD5'],
  ['#2B1C10', '#16100B', '#24170E'],
  ['#FFFFFF', '#F8FAFC', '#EEF2F6'],
  ['#182230', '#0B1018', '#151D28'],
];
const selectedColors = ['#FFFFFF', '#FFF000', '#000000', '#101010', '#14B8A6', '#7F7F7F'];
for (const red of [0, 51, 102, 153, 204, 255]) {
  for (const green of [0, 51, 102, 153, 204, 255]) {
    for (const blue of [0, 51, 102, 153, 204, 255]) {
      selectedColors.push('#' + [red, green, blue].map(value => value.toString(16).padStart(2, '0')).join(''));
    }
  }
}

for (const surface of surfaces) {
  for (const color of selectedColors) {
    const rendered = theme.ensureContrast(color, surface, 4.5);
    assert.ok(
      theme.contrastRatio(rendered, surface) >= 4.5,
      `${color} on ${surface} rendered as ${rendered} below 4.5:1`
    );
    const foreground = theme.textColorFor(rendered);
    assert.ok(
      theme.contrastRatio(foreground, rendered) >= 4.5,
      `${foreground} is not readable on ${rendered}`
    );

    const interactive = theme.ensureContrastOnSurfaces(color, [surface], 4.5, [.1, .12, .16]);
    for (const alpha of [.1, .12, .16]) {
      const tintedSurface = theme.blendHex(surface, interactive, alpha);
      assert.ok(
        theme.contrastRatio(interactive, tintedSurface) >= 4.5,
        `${color} on ${surface} at ${alpha} opacity rendered as ${interactive} below 4.5:1`
      );
    }
  }
}

for (const themeSurfaces of paletteSurfaces) {
  for (const color of selectedColors) {
    const interactive = theme.ensureContrastOnSurfaces(color, themeSurfaces, 4.5, [.1, .12, .16]);
    for (const surface of themeSurfaces) {
      assert.ok(theme.contrastRatio(interactive, surface) >= 4.5, `${interactive} is not readable on ${surface}`);
      for (const alpha of [.1, .12, .16]) {
        assert.ok(
          theme.contrastRatio(interactive, theme.blendHex(surface, interactive, alpha)) >= 4.5,
          `${interactive} is not readable in the ${alpha} interactive state on ${surface}`
        );
      }
    }
  }
}

assert.equal(theme.validHex('#abcdef', '#000000'), '#abcdef');
assert.equal(theme.validHex('not-a-color', '#000000'), '#000000');
assert.equal(theme.blendHex('#000000', '#FFFFFF', 0.5), '#808080');
assert.equal(hotkey.parseVkInput('a'), 0x41);
assert.equal(hotkey.parseVkInput('9'), 0x39);
assert.equal(hotkey.parseVkInput('F24'), 0x87);
assert.equal(hotkey.parseVkInput('space'), 0x20);
assert.equal(hotkey.parseVkInput(''), 0);
assert.equal(hotkey.parseVkInput('Escape'), null);
assert.equal(hotkey.parseVkInput('F25'), null);
assert.equal(hotkey.vkToDisplay(0x20), 'Space');
assert.equal(hotkey.vkToDisplay(0x70), 'F1');
assert.equal(hotkey.vkToDisplay(0x87), 'F24');
assert.equal(hotkey.vkToDisplay(0x1B), '');

const modules = fs.readdirSync(path.join(__dirname, 'app')).filter(name => name.endsWith('.js'));
const moduleSources = new Map(modules.map(name => [name, fs.readFileSync(path.join(__dirname, 'app', name), 'utf8')]));
const applicationSource = [...moduleSources.values()].join('\n');
const apiSource = fs.readFileSync(path.join(__dirname, 'api.js'), 'utf8');
for (const module of modules) {
  const syntax = childProcess.spawnSync(process.execPath, ['--check', path.join(__dirname, 'app', module)], { encoding: 'utf8' });
  assert.equal(syntax.status, 0, syntax.stderr || syntax.stdout || `${module} has invalid syntax`);
}
const stylesheet = fs.readdirSync(__dirname)
  .filter(name => name.endsWith('.css'))
  .map(name => fs.readFileSync(path.join(__dirname, name), 'utf8'))
  .join('\n');
for (const [name, source] of moduleSources) {
  const lines = source.split(/\r?\n/).length;
  assert.ok(lines <= 500, `${name} has ${lines} lines; application modules must stay at or below 500`);
  assert.doesNotMatch(source, /window\.[A-Za-z_$][\w$]*\s*=/, `${name} exports business state through window`);
  assert.doesNotMatch(source, /\.(?:onclick|onchange|oninput|onsubmit|onkeydown|onkeyup|onload)\s*=/, `${name} assigns an event-handler property`);
  for (const access of source.matchAll(/window\.([A-Za-z_$][\w$]*)/g)) {
    assert.ok(
      new Set(['addEventListener', 'clearTimeout', 'clipvaultI18n', 'confirm', 'location', 'matchMedia', 'scrollTo', 'scrollY', 'setTimeout']).has(access[1]),
      `${name} accesses non-platform window member ${access[0]}`
    );
  }
  for (const token of source.matchAll(/["'`]\/api\/[^"'`\s]*/g)) {
    assert.match(token[0], /^["'`]\/api\/v1\//, `${name} contains an unversioned API path ${token[0]}`);
  }
}
assert.match(applicationSource, /ensureContrastOnSurfaces\(app\.validHex\(theme\.primary/);
assert.match(applicationSource, /ensureContrastOnSurfaces\(app\.validHex\(theme\.accent/);
assert.match(applicationSource, /--on-p/, 'the primary foreground token must be set at runtime');
assert.match(stylesheet, /color:\s*var\(--on-p\)/, 'solid primary controls must use the calculated foreground');
assert.match(stylesheet, /\.background-range input\[type=range\]\s*\{[^}]*accent-color:\s*var\(--p\)/, 'background adjustment sliders must use the theme primary color');
assert.doesNotMatch(
  applicationSource,
  /try\s*\{\s*new RegExp\(p\);\s*\}/,
  'sensitive rules must be validated by the Go RE2 backend, not JavaScript RegExp'
);
assert.match(
  applicationSource,
  /protectedFetch\('\/api\/v1\/config'/,
  'config autosave must use the shared secondary authentication request path'
);
assert.match(apiSource, /export function unwrapAPIEnvelope/, 'API error envelopes must have a directly testable decoder');
assert.match(apiSource, /export async function requestWithSecondaryAuth/, 'secondary authentication retry must have one shared implementation');
assert.doesNotMatch(
  applicationSource,
  /res\.status\s*===\s*403|res\.status\s*!==\s*403/,
  'feature modules must not implement ad-hoc HTTP 403 retry logic'
);

const basePairs = selectedColors.length * surfaces.length;
const palettePairs = selectedColors.length * paletteSurfaces.length * 3;
console.log(`theme color contrast acceptance passed (${basePairs} base pairs, ${basePairs * 3 + palettePairs * 4} interactive checks)`);
