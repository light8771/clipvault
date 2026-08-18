const startupToken=location.hash.slice(1);
history.replaceState(null,'',location.pathname);
// 主题注入：把 Go 模板写入 <html> data 属性的当前皮肤主色映射为 CSS 变量；
// 应用设置为固定明/暗时通过 data-theme 强制，system 时交给 prefers-color-scheme。
const docEl=document.documentElement;
if(docEl.dataset.pLight)docEl.style.setProperty('--theme-primary-light',docEl.dataset.pLight);
if(docEl.dataset.pLightHover)docEl.style.setProperty('--theme-primary-light-hover',docEl.dataset.pLightHover);
if(docEl.dataset.pDark)docEl.style.setProperty('--theme-primary-dark',docEl.dataset.pDark);
if(docEl.dataset.pDarkHover)docEl.style.setProperty('--theme-primary-dark-hover',docEl.dataset.pDarkHover);
if(docEl.dataset.themeMode==='light'||docEl.dataset.themeMode==='dark')docEl.dataset.theme=docEl.dataset.themeMode;
const root=document.querySelector('.main');
const mode=root.dataset.mode,isSetup=mode==='set_credentials';
const $=id=>document.getElementById(id);
const els={password:$('password'),confirm:$('confirm'),secondaryPassword:$('secondaryPassword'),secondaryConfirm:$('secondaryConfirm')};
const msgs={password:$('msg-password'),confirm:$('msg-confirm'),secondaryPassword:$('msg-secondaryPassword'),secondaryConfirm:$('msg-secondaryConfirm')};
const submit=$('submit'),errBox=$('error');
const MIN_REC=Number(root.dataset.minRecoveryRunes),MAX_REC_BYTES=Number(root.dataset.maxRecoveryBytes),MIN_SEC=Number(root.dataset.minSecondaryRunes),MAX_SEC_BYTES=Number(root.dataset.maxSecondaryBytes);
const utf8Encoder=new TextEncoder();
const edgeWhitespace=/^\p{White_Space}|\p{White_Space}$/u;
let startupSessionReady=!startupToken;

async function establishStartupSession(){
  if(!startupToken)return;
  try{
    const res=await fetch('/api/v1/startup/session',{method:'POST',headers:{'X-ClipVault-Startup-Token':startupToken}});
    startupSessionReady=res.ok;
    if(!res.ok)errBox.textContent='安全设置链接已失效，请重启 ClipVault 后重试';
  }catch(_){
    startupSessionReady=false;
    errBox.textContent='无法连接本地服务，请确认 ClipVault 仍在运行';
  }
  validate();
}

document.querySelectorAll('.eye').forEach(btn=>{btn.addEventListener('click',()=>{const input=$(btn.dataset.target);const show=input.type==='password';input.type=show?'text':'password';btn.textContent=show?'隐藏':'显示';});});

function setMsg(key,ok,text){const el=msgs[key];if(!el)return;el.textContent=text||'';el.className='msg'+(text?(ok?' ok':' err'):'');}
function mark(key,bad){const el=els[key];if(el)el.classList.toggle('bad',bad);}
function checkPassword(key,min,maxBytes){
  const v=els[key].value;
  if(!v){setMsg(key,false,'');mark(key,false);return false;}
  if(edgeWhitespace.test(v)){setMsg(key,false,'首尾不能有空白字符');mark(key,true);return false;}
  const runeCount=Array.from(v).length;
  if(runeCount<min){setMsg(key,false,'至少 '+min+' 个字符，还差 '+(min-runeCount)+' 个');mark(key,true);return false;}
  if(utf8Encoder.encode(v).length>maxBytes){setMsg(key,false,'密码过长');mark(key,true);return false;}
  setMsg(key,true,'✓ 长度达标');mark(key,false);return true;
}
function checkPair(a,b){const va=els[a].value,vb=els[b].value;if(!vb){setMsg(b,false,'');mark(b,false);return false;}if(va!==vb){setMsg(b,false,'两次输入不一致');mark(b,true);return false;}setMsg(b,true,'✓ 一致');mark(b,false);return true;}

function setStep(id,state){const el=$(id);if(!el)return;el.classList.toggle('is-done',state==='done');el.classList.toggle('is-current',state==='current');}
function updateSteps(recoveryDone,secondaryDone,allDone){
  setStep('step-recovery',recoveryDone?'done':'current');
  setStep('step-secondary',recoveryDone?(secondaryDone?'done':'current'):'');
  setStep('step-finish',allDone?'current':'');
}

function validate(){
  let ok=true;
  if(isSetup){
    const recoveryOK=checkPassword('password',MIN_REC,MAX_REC_BYTES);
    const recoveryPairOK=checkPair('password','confirm');
    const secondaryOK=checkPassword('secondaryPassword',MIN_SEC,MAX_SEC_BYTES);
    const secondaryPairOK=checkPair('secondaryPassword','secondaryConfirm');
    ok=recoveryOK&&recoveryPairOK&&secondaryOK&&secondaryPairOK;
    updateSteps(recoveryOK&&recoveryPairOK,secondaryOK&&secondaryPairOK,ok);
  }else{
    ok=els.password.value.length>0;
  }
  submit.disabled=!ok||!startupSessionReady;
}
Object.values(els).forEach(el=>{if(el)el.addEventListener('input',validate);});
validate();
establishStartupSession();

async function submitCredentials(e){
  if(e)e.preventDefault();
  if(submit.disabled)return;
  errBox.textContent='';submit.disabled=true;
  try{
    const res=await fetch('/api/v1/startup/credentials',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:els.password.value,confirm:els.confirm?els.confirm.value:'',secondaryPassword:els.secondaryPassword?els.secondaryPassword.value:'',secondaryConfirm:els.secondaryConfirm?els.secondaryConfirm.value:''})});
    let data={};try{data=await res.json();}catch(_){data={};}
    if(!res.ok){errBox.textContent=res.status===403?'安全会话已失效，请刷新页面后重试':(data.error?.message||'本地服务请求失败');submit.disabled=false;return;}
    const card=document.querySelector('.main');
    card.innerHTML='<div class="shell"><div class="done-panel"><h1>设置完成</h1><p class="desc">正在打开记录页面...</p><div class="warn"><p class="warn-title">如未自动跳转</p><p class="warn-copy">请点击任务栏右下角 ClipVault 图标 → 右键 → 搜索历史 / 设置</p></div></div></div>';
    let polled=0;const poll=async()=>{try{const r=await fetch('/api/v1/startup/status');const d=await r.json();if(d.data?.ready&&d.data.recordURL){location.href=d.data.recordURL;return;}}catch(_){}if(++polled>30){card.innerHTML='<div class="shell"><div class="done-panel"><h1>设置完成</h1><p class="desc">记录页面未自动打开。</p><div class="warn"><p class="warn-title">手动继续</p><p class="warn-copy">请点击任务栏右下角 ClipVault 图标 → 右键 → 搜索历史 / 设置</p></div></div></div>';return;}setTimeout(poll,500);};poll();
  }catch(_){errBox.textContent='无法连接本地服务，请确认 ClipVault 仍在运行';submit.disabled=false;}
}
document.querySelectorAll('form').forEach(form=>form.addEventListener('submit',submitCredentials));
submit.addEventListener('click',submitCredentials);
