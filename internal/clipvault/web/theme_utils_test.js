'use strict';

const assert = require('node:assert/strict');
const childProcess = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const theme = require('./theme-utils.js');
const hotkey = require('./hotkey-utils.js');

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

const script = fs.readdirSync(path.join(__dirname, 'app'))
  .filter(name => name.endsWith('.js'))
  .sort((a, b) => ['bootstrap.js','auth.js','history.js','files.js','appearance.js','settings.js','config-form.js','transfer.js','groups.js','lifecycle.js'].indexOf(a) - ['bootstrap.js','auth.js','history.js','files.js','appearance.js','settings.js','config-form.js','transfer.js','groups.js','lifecycle.js'].indexOf(b))
  .map(name => fs.readFileSync(path.join(__dirname, 'app', name), 'utf8'))
  .join('\n');
const apiSource = fs.readFileSync(path.join(__dirname, 'api.js'), 'utf8');
const syntaxDir = fs.mkdtempSync(path.join(os.tmpdir(), 'clipvault-module-'));
try {
  const syntaxFile = path.join(syntaxDir, 'application.mjs');
  fs.writeFileSync(syntaxFile, script, 'utf8');
  const syntax = childProcess.spawnSync(process.execPath, ['--check', syntaxFile], { encoding: 'utf8' });
  assert.equal(syntax.status, 0, syntax.stderr || syntax.stdout || 'composed application module has invalid syntax');
} finally {
  fs.rmSync(syntaxDir, { recursive: true, force: true });
}
const stylesheet = fs.readdirSync(__dirname)
  .filter(name => name.endsWith('.css'))
  .map(name => fs.readFileSync(path.join(__dirname, name), 'utf8'))
  .join('\n');
assert.match(script, /ensureContrastOnSurfaces\(validHex\(theme\.primary/);
assert.match(script, /ensureContrastOnSurfaces\(validHex\(theme\.accent/);
assert.match(script, /--on-p/, 'the primary foreground token must be set at runtime');
assert.match(stylesheet, /color:\s*var\(--on-p\)/, 'solid primary controls must use the calculated foreground');
assert.match(stylesheet, /\.background-range input\[type=range\]\s*\{[^}]*accent-color:\s*var\(--p\)/, 'background adjustment sliders must use the theme primary color');
assert.doesNotMatch(
  script,
  /try\s*\{\s*new RegExp\(p\);\s*\}/,
  'sensitive rules must be validated by the Go RE2 backend, not JavaScript RegExp'
);
assert.match(
  script,
  /protectedFetch\('\/api\/v1\/config'/,
  'config autosave must use the shared secondary authentication request path'
);
assert.match(apiSource, /export function unwrapAPIEnvelope/, 'API error envelopes must have a directly testable decoder');
assert.match(apiSource, /export async function requestWithSecondaryAuth/, 'secondary authentication retry must have one shared implementation');
assert.doesNotMatch(
  script,
  /res\.status\s*===\s*403|res\.status\s*!==\s*403/,
  'feature modules must not implement ad-hoc HTTP 403 retry logic'
);

const basePairs = selectedColors.length * surfaces.length;
const palettePairs = selectedColors.length * paletteSurfaces.length * 3;
console.log(`theme color contrast acceptance passed (${basePairs} base pairs, ${basePairs * 3 + palettePairs * 4} interactive checks)`);
