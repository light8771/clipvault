import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs';
import vm from 'node:vm';
import { unsafeHTMLTranslations } from '../../../scripts/check-i18n-html.mjs';

const web = new URL('./', import.meta.url);
const read = name => fs.readFileSync(new URL(name, web), 'utf8');
const payload = `"><button data-action="delete-item">injected & 'text'</button>`;
globalThis.window = {
  fetch() {}, location: { origin: 'http://localhost' },
  clipvaultI18n: { t: () => payload, locale: () => 'en' }
};
const { app } = await import('./app/app-context.js');
const { registerTextUtils } = await import('./app/text-utils.js');
registerTextUtils();

test('HTML translation guard follows syntax and indirect bindings', () => {
  for (const source of [
    `el.innerHTML = t('label');`,
    `el['innerHTML'] = t('label');`,
    `const translate = window.clipvaultI18n.t; el.innerHTML = translate('label');`,
    `el.innerHTML =\n t('label');`,
    `const label = t('label'); el.innerHTML = label;`,
    `const titles = { create: t('create') }; el.innerHTML = titles[mode];`,
    `function label() { return t('label'); } el.innerHTML = label();`,
    `const label = () => t('label'); el.insertAdjacentHTML('beforeend', label());`,
    `el.innerHTML = rows.map(() => { const label = t('label'); return '<p>' + label + '</p>'; }).join('');`,
    `el.innerHTML = '<p>' + t('label') + '</p>';`,
    'const html = `<input title="${t("label")}">`;',
    `el.innerHTML = '<p>' +\n (ok ? t('one') : t('two')) + '</p>';`,
    'const html = `<p>\n ${t("label")}\n</p>`;'
  ]) assert.ok(unsafeHTMLTranslations(source).length, source);
  for (const source of [
    `el.innerHTML = '<p>' + app.esc(value || t('label')) + '</p>';`,
    `el.innerHTML = app.esc(\n t('label')\n);`,
    `const label = t('label'); function render(label) { el.innerHTML = label; }`,
    `const label = t('label'); el.innerHTML = label ? '<p>yes</p>' : '';`
  ]) assert.deepEqual(unsafeHTMLTranslations(source), [], source);
});

test('HTML fragments do not interpolate unescaped translation calls', () => {
  const files = fs.readdirSync(web, { recursive: true })
    .filter(name => name.endsWith('.js') && !name.endsWith('_test.js'));
  const violations = files.flatMap(file => unsafeHTMLTranslations(read(file)).map(line => `${file}:${line}`));
  assert.deepEqual(violations, []);
});

test('i18n retains literal text and parameters; HTML encoding happens at the sink', () => {
  const sandbox = { window: { __clipvaultI18n: { lang: 'en', dict: { label: '{value}' } } } };
  vm.runInNewContext(read('i18n.js'), sandbox);
  assert.equal(sandbox.window.clipvaultI18n.t('label', { value: payload }), payload);
  assert.equal(app.esc(payload), '&quot;&gt;&lt;button data-action=&quot;delete-item&quot;&gt;injected &amp; &#39;text&#39;&lt;/button&gt;');
});

test('modal templates escape malicious translations and indirect labels exactly once', () => {
  let checked = 0;
  for (const file of ['auth', 'recovery', 'transfer', 'settings-storage', 'settings-log-directory']) {
    const source = read(`app/${file}.js`);
    for (const match of source.matchAll(/const html = (`[\s\S]*?`);/g)) {
      for (const enabled of [false, true]) {
        const html = vm.runInNewContext(match[1], {
          app, t: () => payload, title: payload, oldLabel: payload,
          removeWarning: payload, submitLabel: payload, closeSvg: '<svg></svg>',
          isRemove: enabled, isChange: enabled, isReset: enabled
        });
        assert.ok(html.includes(app.esc(payload)), file);
        assert.ok(!html.includes(payload), file);
        assert.ok(!html.includes('&amp;quot;'), file);
        checked++;
      }
    }
  }
  assert.ok(checked >= 10, 'expected all dialog templates to be exercised');
});

test('history metadata escapes translated labels, counts, titles and relative time', () => {
  const template = read('app/history-list.js').match(/return (`<article[\s\S]*?`);/)[1];
  const html = vm.runInNewContext(template, {
    app: { ...app, relTime: () => payload, viewHTML: '', locateHTML: '', cpHTML: '', sensBtnHTML: '', groupBtnHTML: '', delHTML: '' },
    t: () => payload, it: { id: 1, recordType: 'content', starred: false },
    kindKey: 'text', sensAttr: '', bodyHTML: '', kindClass: 'mkind-text',
    kindLabel: payload, duplicateHTML: '', sensHTML: '', fullTime: '',
    charText: payload, srcHTML: '', showView: true, viewAction: 'view-item',
    q: 'search', isEvent: false, canCopy: true, starTitle: payload, starHTML: ''
  });
  assert.ok(!html.includes(payload));
  assert.ok(html.includes(`<span class="mchars">${app.esc(payload)}</span>`));
  assert.ok(html.includes(`title="${app.esc(payload)}"`));
  assert.ok(!html.includes('&amp;quot;'));
});

test('Data API and MCP token rendering escapes indirect state labels', async () => {
  const elements = new Map();
  globalThis.document = { getElementById: id => elements.get(id) || null };
  const { registerDataAPI } = await import('./app/data-api.js');
  const { registerMCP } = await import('./app/mcp.js');
  registerDataAPI();
  registerMCP();
  app.fetch = async () => ({ json: async () => ({ tokens: [{ id: 1, name: 'token', state: 'active', allowSensitive: true }] }) });
  for (const [prefix, load] of [['dapi', app.loadDataAPITokens], ['mcp', app.loadMCPTokens]]) {
    const list = {};
    elements.set(prefix + 'Page', {});
    elements.set(prefix + 'TokenList', list);
    await load();
    assert.ok(list.innerHTML.includes('dapi-card-head'), 'must render the token, not an error fallback');
    assert.ok(list.innerHTML.includes(app.esc(payload)));
    assert.ok(!list.innerHTML.includes(payload));
    assert.ok(!list.innerHTML.includes('&amp;quot;'));
  }
});
