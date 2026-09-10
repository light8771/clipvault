import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = {fetch: async () => {}, location:{origin:'http://localhost'},clipvaultI18n:{t:key=>key}};
const {app} = await import('./app/app-context.js');
const {registerCleanupModal} = await import('./app/cleanup-modal.js');
const deferred = () => {let resolve; const promise=new Promise(r=>resolve=r); return {promise,resolve};};
const response = data => ({ok:true,json:async()=>data});
function setup() {
  const nodes=new Map();
  const node=()=>({disabled:false,checked:true,value:'',textContent:'',children:[],classList:{contains:()=>false,add(){},remove(){},toggle(){}},setAttribute(){},removeAttribute(){},appendChild(n){this.children.push(n);},querySelectorAll:()=>[],addEventListener(){}});
  globalThis.document={body:{style:{}},createElement:node,getElementById(id){if(!nodes.has(id))nodes.set(id,node());return nodes.get(id);},querySelector:()=>({value:'all'}),querySelectorAll:()=>[],addEventListener(){}};
  registerCleanupModal(); app.toast=()=>{};
  return nodes;
}

test('obsolete cleanup preview cannot enable deletion for changed conditions',async()=>{
  const nodes=setup(); const first=deferred(),second=deferred(); let calls=0;
  app.protectedFetch=()=>++calls===1?first.promise:second.promise;
  const old=app.refreshCleanupPreview(); nodes.get('cleanupPreserveStarred').checked=false;
  const current=app.refreshCleanupPreview(); second.resolve(response({matched:0}));await current;
  first.resolve(response({matched:10}));await old;
  assert.equal(app.cleanupMatched,0);assert.equal(app.CLEANUP_CONFIRM.disabled,true);
});

test('cleanup cannot delete unpreviewed conditions or submit twice',async()=>{
  const nodes=setup(); let deletes=0;const pending=deferred();
  app.protectedFetch=async url=>{if(url.endsWith('/cleanup')){deletes++;return pending.promise;}return response({matched:3});};
  await app.refreshCleanupPreview();nodes.get('cleanupPreserveStarred').checked=false;
  await app.doCleanup();assert.equal(deletes,0);
  await app.refreshCleanupPreview();
  const first=app.doCleanup();const second=app.doCleanup();
  assert.equal(deletes,1);pending.resolve(response({ok:true,deleted:3}));await Promise.all([first,second]);
});

test('closing cleanup invalidates outstanding previews',async()=>{
  setup(); const pending=deferred();app.protectedFetch=()=>pending.promise;
  const request=app.refreshCleanupPreview();app.closeCleanupModal();
  pending.resolve(response({matched:9}));await request;
  assert.equal(app.cleanupMatched,0);assert.equal(app.CLEANUP_CONFIRM.disabled,true);
});

test('cancelled cleanup authentication does not immediately prompt again',async()=>{
  setup(); let calls=0;
  app.protectedFetch=async()=>{calls++;if(calls===1)return response({matched:2});throw Object.assign(new Error('cancelled'),{code:'secondary_cancelled'});};
  await app.refreshCleanupPreview();await app.doCleanup();
  assert.equal(calls,2);assert.equal(app.cleanupMatched,0);assert.equal(app.CLEANUP_CONFIRM.disabled,true);
});
