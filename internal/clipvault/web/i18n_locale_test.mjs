import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('./i18n.js', import.meta.url), 'utf8');

test('custom translation IDs never break date rendering or change the dictionary', () => {
  for (const lang of ['zhcn', 'my_pack', 'private', '', null]) {
    const state = { lang, dict: { label: '自定义译文' } };
    const sandbox = { window: { __clipvaultI18n: state, navigator: { languages: ['zh-CN'] } } };
    vm.runInNewContext(source, sandbox);
    const i18n = sandbox.window.clipvaultI18n;
    assert.equal(i18n.locale(), 'zh-CN');
    assert.doesNotThrow(() => new Date().toLocaleString(i18n.locale()));
    assert.equal(i18n.t('label'), '自定义译文');
    assert.equal(state.lang, lang);
  }
});

test('valid locales are preserved and invalid browser preferences have a safe fallback', () => {
  for (const [lang, languages, expected] of [
    ['en-US', ['zh-CN'], 'en-US'], ['ja-JP', ['en-US'], 'ja-JP'],
    ['zhcn', ['invalid_tag', 'en-US'], 'en-US'], ['zhcn', [], 'zh-CN']
  ]) {
    const sandbox = { window: { __clipvaultI18n: { lang, dict: {} }, navigator: { languages } } };
    vm.runInNewContext(source, sandbox);
    assert.equal(sandbox.window.clipvaultI18n.locale(), expected);
  }
});
