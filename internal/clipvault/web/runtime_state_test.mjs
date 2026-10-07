import assert from 'node:assert/strict';
import test from 'node:test';
globalThis.window = {fetch() {}, location:{origin:'http://localhost'}, clipvaultI18n:{t:key=>key}};
const {app} = await import('./app/app-context.js');
const {registerRuntimeState} = await import('./app/runtime-state.js');
function setup() {
 const elements = new Map();
 globalThis.document = {getElementById(id) {
  if (!elements.has(id)) elements.set(id, {textContent:'unchanged', listeners:{}, addEventListener(name,fn){this.listeners[name]=fn;}});
  return elements.get(id);
 }};
 registerRuntimeState();
 app.toast = () => {};
 return {click:()=>app.TOG.listeners.click(), elements};
}
test('shared runtime button works without history initialization and suppresses repeated toggles', async () => {
 const {click} = setup();
 let resolve, calls = 0;
 app.fetch = (url, init) => {calls++;assert.equal(url,'/api/v1/runtime/pause');assert.equal(init.method,'POST');return new Promise(done=>{resolve=done;});};
 const first=click();
 await click();
 assert.equal(calls,1);
 assert.equal(app.TOG.disabled,true);
 assert.equal(await app.refreshState(),false);
 resolve({ok:true,json:async()=>({paused:true})});
 await first;
 assert.equal(app.ST.textContent,'runtime.paused');
 assert.equal(app.TOGTX.textContent,'nav.resume');
 assert.equal(app.TOG.disabled,false);
});
test('old background refresh cannot overwrite a completed pause operation', async () => {
 const {click} = setup();
 let finish;
 app.fetch = url => url==='/api/v1/runtime' ? new Promise(resolve=>{finish=resolve;}) : Promise.resolve({ok:true,json:async()=>({paused:true})});
 const old=app.refreshState();
 await click();
 finish({ok:true,json:async()=>({paused:false})});
 assert.equal(await old,false);
 assert.equal(app.ST.textContent,'runtime.paused');
});
for (const response of [{ok:false,json:async()=>({error:'failed'})},{ok:true,json:async()=>({})}]) {
 test(`invalid runtime response (HTTP ok=${response.ok}) preserves displayed state`, async () => {
  setup();
  app.fetch=async()=>response;
  assert.equal(await app.refreshState(),false);
  assert.equal(app.ST.textContent,'unchanged');
 });
}
test('failed toggle leaves the displayed state unchanged and permits retry', async () => {
 const {click}=setup();
 let calls=0;
 const messages=[];
 app.toast=message=>messages.push(message);
 app.fetch=async()=>{if(++calls===1)throw Error('offline');return {ok:true,json:async()=>({paused:false})};};
 await click();
 assert.equal(app.ST.textContent,'unchanged');
 assert.equal(app.TOG.disabled,false);
 assert.deepEqual(messages,['common.action_failed']);
 await click();
 assert.equal(app.ST.textContent,'runtime.recording');
});
