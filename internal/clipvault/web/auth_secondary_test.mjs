import assert from 'node:assert/strict';
import test from 'node:test';
globalThis.window = {fetch:globalThis.fetch,location:{origin:'http://localhost'},clipvaultI18n:{t:key=>key}};
const {app} = await import('./app/app-context.js');
const {registerAuth} = await import('./app/auth.js');
function setup() {
 const nodes = new Map();
 const make = () => ({value:'',disabled:false,listeners:{},children:[],classList:{add(){},remove(){}},focus(){},setAttribute(){},removeAttribute(){},appendChild(node){this.children.push(node);},querySelectorAll(){return [];},addEventListener(name,fn){this.listeners[name]=fn;}});
 globalThis.document = {activeElement:null,createElement:make,getElementById(id){if(!nodes.has(id))nodes.set(id,make());return nodes.get(id);}};
 registerAuth();
 app.authState.hasSecondaryPassword = true;
 app.toast = () => {};
 return {submit:()=>app.SECONDARY_FORM.listeners.submit({preventDefault(){}})};
}

test('secondary authentication rejects duplicate submits and stale success cannot authorize a new operation', async () => {
 const {submit} = setup();
 const requests=[];
 app.fetch = () => new Promise(resolve=>requests.push(resolve));
 const first = app.requestSecondaryAuth(true);
 const oldSubmit = submit();
 await submit();
 assert.equal(requests.length,1);
 app.closeSecondaryModal();
 assert.equal(await first,false);
 const next = app.requestSecondaryAuth(true);
 let nextResolved = false;
 next.then(()=>{nextResolved=true;});
 const newSubmit = submit();
 assert.equal(requests.length,2);
 requests[0]({ok:true,json:async()=>({ok:true,secondaryToken:'stale'})});
 await oldSubmit;
 assert.equal(nextResolved,false);
 assert.equal(app.pendingSecondaryToken,null);
 await submit();
 assert.equal(requests.length,2,'old response must not release new submission lock');
 requests[1]({ok:true,json:async()=>({ok:true,secondaryToken:'current'})});
 await newSubmit;
 assert.equal(await next,true);
 assert.equal(app.pendingSecondaryToken,'current');
 assert.equal(app.SECONDARY_INPUT.value,'');
});

test('failed verification releases submission lock and retry can complete the same operation', async () => {
 const {submit} = setup();
 let calls=0;
 app.fetch = async()=>{calls++;if(calls===1)throw Error('network');return {ok:true,json:async()=>({ok:true})};};
 const pending=app.requestSecondaryAuth(true);
 await submit();
 assert.equal(app.SECONDARY_ERR.textContent,'common.network_error');
 await submit();
 assert.equal(await pending,true);
 assert.equal(calls,2);
});

const sessionStatus = secondaryAuthed => ({hasLockPassword: true, hasSecondaryPassword: true, authenticated: true, secondaryAuthed});
for (const [name, response] of [
 ['HTTP failure', {ok: false, json: async () => ({ok: false, error: 'unavailable'})}],
 ['malformed success', {ok: true, json: async () => ({authenticated: true})}]
]) {
 test(`session ${name} preserves the lock and current authentication state`, async () => {
  setup();
  const before = {...app.authState};
  app.hideLockScreen = () => assert.fail('invalid response hid lock screen');
  app.fetch = async () => response;
  assert.equal(await app.checkAuthStatus(), false);
  assert.deepEqual(app.authState, before);
 });
}

test('late session status cannot overwrite a newer locked session', async () => {
 setup();
 const pending = [];
 let hidden = 0;
 app.hideLockScreen = () => hidden++;
 app.fetch = () => new Promise(resolve => pending.push(resolve));
 const old = app.checkAuthStatus();
 const current = app.checkAuthStatus();
 pending[1]({ok: true, json: async () => ({...sessionStatus(false), authenticated: false})});
 assert.equal(await current, false);
 pending[0]({ok: true, json: async () => sessionStatus(true)});
 assert.equal(await old, false);
 assert.equal(app.authState.authenticated, false);
 assert.equal(app.authState.secondaryAuthed, false);
 assert.equal(hidden, 0);
});

test('explicit secondary expiry invalidates an outstanding session response', async () => {
 setup();
 let finish;
 app.fetch = () => new Promise(resolve => {finish = resolve;});
 const pending = app.checkAuthStatus();
 app.expireSecondaryAuth();
 finish({ok: true, json: async () => sessionStatus(true)});
 assert.equal(await pending, false);
 assert.equal(app.authState.secondaryAuthed, false);
});

test('fresh secondary verification cannot be undone by an earlier status response', async () => {
 const {submit} = setup();
 let finish;
 app.fetch = url => url === '/api/v1/session'
  ? new Promise(resolve => {finish = resolve;})
  : Promise.resolve({ok: true, json: async () => ({ok: true, secondaryCached: true})});
 const status = app.checkAuthStatus();
 const auth = app.requestSecondaryAuth(true);
 await submit();
 assert.equal(await auth, true);
 finish({ok: true, json: async () => sessionStatus(false)});
 assert.equal(await status, false);
 assert.equal(app.authState.secondaryAuthed, true);
});

test('locking cancels pending secondary verification and discards its late success', async () => {
 const {submit} = setup();
 let finish;
 app.fetch = () => new Promise(resolve => {finish = resolve;});
 const auth = app.requestSecondaryAuth(true);
 const verification = submit();
 app.showLockScreen();
 assert.equal(await auth, false);
 finish({ok: true, json: async () => ({ok: true, secondaryCached: true, secondaryToken: 'obsolete'})});
 await verification;
 assert.equal(app.authState.secondaryAuthed, false);
 assert.equal(app.pendingSecondaryToken, null);
});

test('confirmed secondary expiry clears already displayed history before a refresh', async () => {
 setup();
 app.authState = sessionStatus(true);
 let cleared = 0;
 app.clearHistoryContent = () => cleared++;
 app.fetch = async () => ({ok: true, json: async () => sessionStatus(false)});
 try {
  assert.equal(await app.checkAuthStatus(), true);
  assert.equal(cleared, 1, 'private cards and open previews must not wait for another request');
 } finally { delete app.clearHistoryContent; }
});

test('locking clears history even when no secondary cache was active', () => {
 setup();
 let cleared = 0;
 app.clearHistoryContent = () => cleared++;
 try {
  app.showLockScreen();
  assert.equal(cleared, 1, 'one-time authorized previews must be closed on lock');
 } finally { delete app.clearHistoryContent; }
});

test('late login success cannot reopen a session that was locked again', async () => {
 setup();
 let finish, initialized = 0, hidden = 0;
 app.fetch = () => new Promise(resolve => { finish = resolve; });
 app.init = () => initialized++;
 app.hideLockScreen = () => hidden++;
 const login = app.LOCK_FORM.listeners.submit({ preventDefault() {} });
 app.showLockScreen();
 finish({ ok: true, json: async () => ({ ok: true }) });
 await login;
 assert.equal(initialized, 0);
 assert.equal(hidden, 0);
 assert.equal(app.authState.authenticated, false);
});

test('lock login ignores duplicate submissions until the response body is consumed', async () => {
 setup();
 let finish, requests = 0;
 app.fetch = async () => {
  requests++;
  return { ok: false, json: () => new Promise(resolve => { finish = resolve; }) };
 };
 const submit = () => app.LOCK_FORM.listeners.submit({ preventDefault() {} });
 const first = submit();
 await new Promise(setImmediate);
 const duplicate = submit();
 assert.equal(requests, 1);
 finish({ ok: false, error: 'wrong password' });
 await Promise.all([first, duplicate]);
 app.fetch = async () => { requests++; return { ok: false, json: async () => ({ ok: false }) }; };
 await submit();
 assert.equal(requests, 2, 'a completed failure must permit retry');
});

test('old login failure cannot release a newer submission or replace its feedback', async () => {
 setup();
 const requests = [];
 app.fetch = () => new Promise(resolve => requests.push(resolve));
 const submit = () => app.LOCK_FORM.listeners.submit({ preventDefault() {} });
 const old = submit();
 app.showLockScreen();
 const current = submit();
 requests[0]({ ok: false, json: async () => ({ error: 'obsolete error' }) });
 await old;
 await submit();
 assert.equal(requests.length, 2);
 assert.notEqual(app.LOCK_ERR.textContent, 'obsolete error');
 requests[1]({ ok: false, json: async () => ({ error: 'current error' }) });
 await current;
 assert.equal(app.LOCK_ERR.textContent, 'current error');
});

for (const entry of ['status', 'protected operation', 'explicit expiry', 'lock']) {
 test(`${entry} removes privileged source metadata immediately on access loss`, async () => {
  setup();
  app.authState = sessionStatus(true);
  let cleared = 0;
  app.invalidateSourceExes = () => cleared++;
  app.fetch = async () => ({ ok: true, json: async () => sessionStatus(false) });
  try {
   if (entry === 'status') await app.checkAuthStatus();
   else if (entry === 'explicit expiry') app.expireSecondaryAuth();
   else if (entry === 'lock') app.showLockScreen();
   else {
    const pending = app.requestSecondaryAuth();
    await new Promise(setImmediate);
    app.closeSecondaryModal();
    assert.equal(await pending, false);
   }
   assert.equal(cleared, 1, 'metadata must not remain visible until a later network refresh');
  } finally { delete app.invalidateSourceExes; }
 });
}
