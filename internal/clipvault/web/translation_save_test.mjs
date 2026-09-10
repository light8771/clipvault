import assert from 'node:assert/strict';
import test from 'node:test';
globalThis.window={fetch:globalThis.fetch,location:{origin:'http://localhost'},clipvaultI18n:{t:key=>key}};
const {app}=await import('./app/app-context.js');
const {bindTranslationSave}=await import('./app/translation-save.js');
function setup() {
 const input={value:'original',classList:{toggle(){}},setAttribute(){},removeAttribute(){},addEventListener(name,fn){this[name]=fn;}};
 const entry={key:'common.save',customText:'original'};
 bindTranslationSave({input,entry,row:{classList:{toggle(){}}},status:{},languageCode:'ja-JP'});
 app.toast=()=>{};
 return {input,entry};
}
test('successful saves advance the baseline so changing back to the original text is saved',async()=>{
 const {input,entry}=setup();const values=[];
 app.protectedFetch=async(url,init)=>{values.push(JSON.parse(init.body).value);return {ok:true};};
 input.value='changed';await input.blur();
 input.value='original';await input.blur();
 await input.blur();
 assert.deepEqual(values,['changed','original']);assert.equal(entry.customText,'original');
});
test('edits blurred during an in-flight save are queued and preserve whitespace and newlines',async()=>{
 const {input,entry}=setup();const pending=[],requests=[];
 app.protectedFetch=(url,init)=>{requests.push({url,value:JSON.parse(init.body).value});return new Promise(resolve=>pending.push(resolve));};
 input.value='first';const saving=input.blur();
 input.value='  second\nline  ';input.blur();
 assert.equal(requests.length,1);
 pending.shift()({ok:true});await Promise.resolve();
 assert.equal(requests.length,2);assert.equal(requests[1].value,'  second\nline  ');
 assert.match(requests[1].url,/lang=ja-JP&key=common.save/);
 pending.shift()({ok:true});await saving;
 assert.equal(entry.customText,input.value);
});
test('failed saves retain user input and blur retries the same value',async()=>{
 const {input,entry}=setup();let calls=0;
 app.protectedFetch=async()=>{if(++calls===1)throw Error('offline');return {ok:true};};
 input.value='unsaved';await input.blur();
 assert.equal(input.value,'unsaved');assert.equal(entry.customText,'original');
 await input.blur();assert.equal(entry.customText,'unsaved');assert.equal(calls,2);
});
