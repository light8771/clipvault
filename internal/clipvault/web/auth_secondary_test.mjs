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
