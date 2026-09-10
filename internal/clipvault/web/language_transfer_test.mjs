import assert from 'node:assert/strict';
import test from 'node:test';
globalThis.window = { fetch: globalThis.fetch, location: { origin: 'http://localhost' }, clipvaultI18n: { t: key => key } };
const { app } = await import('./app/app-context.js');
const { registerLanguagePackImport } = await import('./app/language-transfer.js');

function fixture() {
  const ids = ['langPackForm','langPackFile','langPackPreview','langPackSummary','langPackCode','langPackName','langPackNative','langPackBase','langPackMerge'];
  const node = () => ({ value: '', disabled: false, hidden: false, children: [], listeners: {},
    setAttribute() {}, removeAttribute() {}, appendChild(child) { this.children.push(child); },
    addEventListener(event, fn) { this.listeners[event] = fn; }, reportValidity: () => true,
    querySelectorAll: () => [], reset() { this.wasReset = true; } });
  const nodes = Object.fromEntries(ids.map(id => [id,node()]));
  nodes.langPackForm.parentElement = node();
  globalThis.document = { createElement: node, getElementById: id => nodes[id] };
  const pack = { format:'clipvault-language', version:1, language:{code:'ja-JP',name:'Japanese',nativeName:'日本語',baseLanguageCode:'en-US'}, translations:{'common.save':'保存'} };
  nodes.langPackFile.files = [{ size:100, text: async () => JSON.stringify(pack) }];
  return { nodes, status: () => nodes.langPackForm.parentElement.children[0] };
}

test('choosing a pack before languages load waits for metadata before showing the preview', async () => {
  const { nodes, status } = fixture();
  let languages = [], finishLoading;
  registerLanguagePackImport(() => languages, () => new Promise(resolve => { finishLoading = () => { languages = [{code:'en-US',isBuiltin:true,name:'English'}]; resolve(true); }; }));
  const pending = nodes.langPackFile.listeners.change();
  await Promise.resolve();
  assert.equal(nodes.langPackPreview.hidden, true);
  assert.equal(status().hidden, false);
  finishLoading();
  await pending;
  assert.equal(nodes.langPackPreview.hidden, false);
  assert.equal(nodes.langPackBase.children[0].value, 'en-US');
});

test('a committed import with a failed list refresh reports success-with-warning and cannot be submitted twice', async () => {
  const { nodes, status } = fixture();
  registerLanguagePackImport(() => [{code:'en-US',isBuiltin:true,name:'English'}], async () => false);
  await nodes.langPackFile.listeners.change();
  let requests = 0;
  app.protectedFetch = async () => { requests++; return {ok:true,json:async()=>({ok:true,count:1})}; };
  const submit = () => nodes.langPackForm.listeners.submit({preventDefault() {}});
  await submit();
  assert.equal(status().textContent, 'i18n.pack.refresh_failed');
  assert.equal(nodes.langPackForm.wasReset, true);
  assert.equal(nodes.langPackPreview.hidden, true);
  await submit();
  assert.equal(requests, 1);
});

test('rejected imports retain the selected pack and can retry without choosing the file again', async () => {
  const { nodes, status } = fixture();
  registerLanguagePackImport(() => [{code:'en-US',isBuiltin:true,name:'English'}], async () => true);
  await nodes.langPackFile.listeners.change();
  let requests = 0;
  app.protectedFetch = async () => { requests++; return {ok:requests>1,json:async()=>({ok:true,count:1})}; };
  app.readAPIError = async () => ({message:'Rejected'});
  const submit = () => nodes.langPackForm.listeners.submit({preventDefault() {}});
  await submit();
  assert.equal(status().textContent, 'Rejected');
  assert.equal(nodes.langPackPreview.hidden, false);
  await submit();
  assert.equal(requests, 2);
  assert.equal(nodes.langPackForm.wasReset, true);
});

test('older language responses and failures cannot overwrite a newer list', async () => {
  const { createLanguageLoader } = await import('./app/language-data.js');
  const pending = [], rendered = [], errors = [];
  app.fetch = () => new Promise((resolve,reject) => pending.push({resolve,reject}));
  app.toast = message => errors.push(message);
  const load = createLanguageLoader(data => rendered.push(data));
  const old = load(), latest = load();
  pending[1].resolve({ok:true,json:async()=>({languages:['new']})});
  assert.equal(await latest,true);
  pending[0].resolve({ok:true,json:async()=>({languages:['old']})});
  assert.equal(await old,false);
  assert.deepEqual(rendered,[{languages:['new']}]);
  const failure = load(), refresh = load();
  pending[3].resolve({ok:true,json:async()=>({languages:['newest']})});
  await refresh;
  pending[2].reject(new Error('stale failure'));
  await failure;
  assert.deepEqual(errors,[]);
});

test('file read failure clears the input so choosing the same file triggers a retry', async () => {
  const { nodes, status } = fixture();
  registerLanguagePackImport(() => [], async () => false);
  nodes.langPackFile.value = 'selected.json';
  await nodes.langPackFile.listeners.change();
  assert.equal(nodes.langPackFile.value,'');
  assert.equal(nodes.langPackPreview.hidden,true);
  assert.equal(status().hidden,false);
});

test('cancelled authentication restores the form and keeps the pack available for retry', async () => {
  const { nodes, status } = fixture();
  registerLanguagePackImport(() => [{code:'en-US',isBuiltin:true,name:'English'}], async () => true);
  await nodes.langPackFile.listeners.change();
  app.protectedFetch = async () => { throw new app.APIError('secondary_cancelled','Cancelled',403); };
  await nodes.langPackForm.listeners.submit({preventDefault(){}});
  assert.equal(status().textContent,'Cancelled');
  assert.equal(nodes.langPackForm.wasReset,undefined);
  assert.equal(nodes.langPackPreview.hidden,false);
  app.protectedFetch = async () => ({ok:true,json:async()=>({ok:true,count:1})});
  await nodes.langPackForm.listeners.submit({preventDefault(){}});
  assert.equal(nodes.langPackForm.wasReset,true);
});

test('entry loader isolates languages, filters and hidden editors from late responses', async () => {
 const {createLanguageEntriesLoader}=await import('./app/language-data.js');
 const pending=[],rendered=[],errors=[];
 let state={code:'ja-JP',page:1,pageSize:100,query:'first'},visible=true;
 app.fetch=url=>new Promise((resolve,reject)=>pending.push({url,resolve,reject}));
 app.toast=message=>errors.push(message);
 const load=createLanguageEntriesLoader(()=>state,()=>visible,data=>rendered.push(data));
 const old=load();state={...state,code:'fr-FR',query:'second'};const next=load();
 assert.match(pending[1].url,/lang=fr-FR/);assert.match(pending[1].url,/q=second/);
 pending[1].resolve({ok:true,json:async()=>({language:{code:'fr-FR'}})});await next;
 pending[0].resolve({ok:true,json:async()=>({language:{code:'ja-JP'}})});await old;
 assert.deepEqual(rendered,[{language:{code:'fr-FR'}}]);
 const hidden=load();visible=false;pending[2].resolve({ok:true,json:async()=>({old:true})});await hidden;
 assert.equal(rendered.length,1);
 visible=true;const failed=load();pending[3].reject(Error('offline'));await failed;
 assert.deepEqual(errors,['offline']);
});
