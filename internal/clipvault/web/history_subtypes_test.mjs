import assert from 'node:assert/strict';
import test from 'node:test';

class Element {
  constructor(tag = 'div') {
    this.tagName = tag;
    this.children = [];
    this.listeners = {};
    this.attributes = {};
    this.style = {};
    this.value = '';
  }
  addEventListener(type, listener) { (this.listeners[type] ??= []).push(listener); }
  async emit(type, event = {}) {
    for (const listener of this.listeners[type] ?? []) await listener({target: this, stopPropagation() {}, ...event});
  }
  append(...children) { children.forEach(child => this.appendChild(child)); }
  appendChild(child) { this.children.push(child); child.parent = this; }
  before(element) { this.wrapper = element; }
  replaceChildren() { this.children = []; }
  setAttribute(key, value) { this.attributes[key] = value; }
  getAttribute(key) { return this.attributes[key]; }
  contains(target) { return this === target || this.children.some(child => child.contains(target)); }
  closest() { return this.parent; }
  getBoundingClientRect() { return {left: 20, right: 150, top: 100, bottom: 130}; }
  querySelectorAll() { return this.children.filter(child => child.tagName === 'button'); }
  focus() {
    document.activeElement = this;
    for (const listener of document.listeners.focusin ?? []) listener({target: this});
  }
}

globalThis.window = {
  fetch() {}, location: {origin: 'http://localhost'},
  innerWidth: 1200, innerHeight: 800,
  matchMedia: () => ({matches: false}),
  addEventListener() {}, clipvaultI18n: {t: key => key}
};
globalThis.document = new Element();
document.createElement = tag => new Element(tag);
document.body = new Element();
const {app} = await import('./app/app-context.js');
const {setupHistorySubtypes} = await import('./app/history-subtypes.js');

function setup() {
  document.listeners = {};
  document.body = new Element();
  const chips = new Map(['text', 'image', 'video', 'archive', 'office', 'files'].map(kind => {
    const chip = new Element('button'); chip.textContent = kind; return [kind, chip];
  }));
  setupHistorySubtypes({querySelector: selector => chips.get(selector.match(/"(.*?)"/)[1])}, () => {});
  const popup = document.body.children[0];
  const arrow = chips.get('image').wrapper.children[1];
  app.requireOK = async response => response;
  return {popup, arrow, search: popup.children[0], list: popup.children[1]};
}

test('leaving a loading subtype menu cancels it and late data cannot steal focus', async () => {
  const {popup, arrow, list} = setup();
  let resolve, signal;
  app.fetch = (_, init) => { signal = init.signal; return new Promise(done => { resolve = done; }); };
  arrow.focus();
  const opening = arrow.emit('click');
  assert.equal(popup.hidden, false);
  const outside = new Element('button');
  outside.focus();
  assert.equal(popup.hidden, true);
  assert.equal(signal.aborted, true);
  resolve({json: async () => ['ext:png']});
  await opening;
  assert.equal(document.activeElement, outside);
  assert.equal(list.children.length, 0);
  assert.equal(arrow.getAttribute('aria-expanded'), 'false');
});

test('IME candidate navigation and Escape do not control the subtype menu', async () => {
  const {popup, arrow, search, list} = setup();
  app.fetch = async () => ({json: async () => ['ext:png']});
  arrow.focus();
  await arrow.emit('click');
  assert.equal(document.activeElement, search);
  for (const key of ['ArrowUp', 'ArrowDown', 'Home', 'End', 'Escape']) {
    const event = {target: search, key, isComposing: true, preventDefault() { assert.fail('IME key intercepted'); }};
    await popup.emit('keydown', event);
    await document.emit('keydown', event);
    assert.equal(document.activeElement, search);
    assert.equal(popup.hidden, false);
  }
  let prevented = false;
  await popup.emit('keydown', {target: search, key: 'ArrowDown', preventDefault() { prevented = true; }});
  assert.equal(prevented, true);
  assert.equal(document.activeElement, list.children[0]);
  await popup.emit('keydown', {key: 'Escape', preventDefault() {}});
  assert.equal(popup.hidden, true);
  assert.equal(document.activeElement, arrow);
});

test('a cancelled response cannot replace options after the menu is reopened', async () => {
  const {popup, arrow, search, list} = setup();
  const pending = [];
  app.fetch = () => new Promise(resolve => pending.push(resolve));
  arrow.focus();
  const first = arrow.emit('click');
  new Element('button').focus();
  arrow.focus();
  const second = arrow.emit('click');
  pending[1]({json: async () => ['ext:png']});
  await second;
  assert.deepEqual(list.children.map(button => button.textContent), ['common.all', 'PNG']);
  pending[0]({json: async () => ['ext:jpg']});
  await first;
  assert.deepEqual(list.children.map(button => button.textContent), ['common.all', 'PNG']);
  assert.equal(document.activeElement, search);
  assert.equal(popup.hidden, false);
  new Element('button').focus();
  assert.equal(popup.hidden, true);
});
