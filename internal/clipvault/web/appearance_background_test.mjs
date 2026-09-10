import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = { fetch: async () => {}, location: { origin: 'http://localhost' } };
const { app } = await import('./app/app-context.js');
const { registerAppearanceBackground } = await import('./app/appearance-background.js');

test('visual changes preserve video playback; replacing media loads the new source', () => {
  let source = '', loads = 0, plays = 0;
  const video = {
    currentTime: 0,
    getAttribute: () => source,
    set src(value) { source = value; loads++; this.currentTime = 0; },
    play: () => { plays++; return Promise.resolve(); },
    pause() {}, load() {}, removeAttribute: () => { source = ''; }
  };
  const elements = {
    backgroundVideo: video,
    backgroundImage: { removeAttribute() {} },
    backgroundMediaLayer: { classList: { toggle() {} } },
    backgroundShield: { classList: { toggle() {} } }
  };
  globalThis.document = {
    documentElement: { style: { setProperty() {} } },
    body: { classList: { toggle() {} } },
    getElementById: id => elements[id]
  };
  registerAppearanceBackground();
  app.applyBackground({ contentType: 'video/mp4', version: 1 });
  video.currentTime = 42;
  app.applyBackground({ contentType: 'video/mp4', version: 1, brightness: 120, opacity: 50 });
  assert.equal(video.currentTime, 42);
  assert.equal(loads, 1);
  assert.equal(plays, 1);
  app.applyBackground({ contentType: 'video/webm', version: 2 });
  assert.equal(source, '/background/media?v=2');
  assert.equal(loads, 2);
  assert.equal(plays, 2);
});
