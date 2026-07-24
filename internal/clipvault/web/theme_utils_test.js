'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const theme = require('./theme-utils.js');

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

const script = fs.readFileSync(path.join(__dirname, 'script.js'), 'utf8');
const stylesheet = fs.readFileSync(path.join(__dirname, 'style.css'), 'utf8');
assert.match(script, /ensureContrastOnSurfaces\(validHex\(theme\.primary/);
assert.match(script, /ensureContrastOnSurfaces\(validHex\(theme\.accent/);
assert.match(script, /--on-p/, 'the primary foreground token must be set at runtime');
assert.match(stylesheet, /color:\s*var\(--on-p\)/, 'solid primary controls must use the calculated foreground');
assert.match(stylesheet, /\.background-range input\[type=range\]\s*\{[^}]*accent-color:\s*var\(--p\)/, 'background adjustment sliders must use the theme primary color');

const basePairs = selectedColors.length * surfaces.length;
const palettePairs = selectedColors.length * paletteSurfaces.length * 3;
console.log(`theme color contrast acceptance passed (${basePairs} base pairs, ${basePairs * 3 + palettePairs * 4} interactive checks)`);
