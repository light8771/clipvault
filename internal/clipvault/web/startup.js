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
// i18n 资源异常缺失时回退 key 原文，避免 validate() 整体崩溃导致提交按钮永久禁用。
const t=(key,params)=>window.clipvaultI18n?window.clipvaultI18n.t(key,params):key;
const root=document.querySelector('.main');
const mode=root.dataset.mode,isSetup=mode==='set_credentials';
const $=id=>document.getElementById(id);
const els={password:$('password'),confirm:$('confirm'),secondaryPassword:$('secondaryPassword'),secondaryConfirm:$('secondaryConfirm'),lockPassword:$('lockPassword'),lockConfirm:$('lockConfirm')};
const msgs={password:$('msg-password'),confirm:$('msg-confirm'),secondaryPassword:$('msg-secondaryPassword'),secondaryConfirm:$('msg-secondaryConfirm'),lockPassword:$('msg-lockPassword'),lockConfirm:$('msg-lockConfirm')};
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
    if(!res.ok)errBox.textContent=t('startup.link_expired');
  }catch(_){
    startupSessionReady=false;
    errBox.textContent=t('startup.connect_failed');
  }
  validate();
}

document.querySelectorAll('.eye').forEach(btn=>{btn.addEventListener('click',()=>{const input=$(btn.dataset.target);const show=input.type==='password';input.type=show?'text':'password';btn.textContent=show?t('startup.hide'):t('startup.show');});});

function setMsg(key,ok,text){const el=msgs[key];if(!el)return;el.textContent=text||'';el.className='msg'+(text?(ok?' ok':' err'):'');}
function mark(key,bad){const el=els[key];if(el)el.classList.toggle('bad',bad);}
function checkPassword(key,min,maxBytes){
  const v=els[key].value;
  if(!v){setMsg(key,false,'');mark(key,false);return false;}
  if(edgeWhitespace.test(v)){setMsg(key,false,t('startup.no_edge_whitespace'));mark(key,true);return false;}
  const runeCount=Array.from(v).length;
  if(runeCount<min){setMsg(key,false,t('startup.need_more_chars',{need:min,remaining:min-runeCount}));mark(key,true);return false;}
  if(utf8Encoder.encode(v).length>maxBytes){setMsg(key,false,t('startup.password_too_long'));mark(key,true);return false;}
  setMsg(key,true,t('startup.length_ok'));mark(key,false);return true;
}
function checkPair(a,b){const va=els[a].value,vb=els[b].value;if(!vb){setMsg(b,false,'');mark(b,false);return false;}if(va!==vb){setMsg(b,false,t('startup.mismatch'));mark(b,true);return false;}setMsg(b,true,t('startup.match_ok'));mark(b,false);return true;}
// 可选密码（锁屏）：留空 = 明确跳过；填写则按与必填密码相同的规则校验。
function checkOptionalPassword(key,min,maxBytes){
  if(!els[key].value){setMsg(key,false,'');mark(key,false);return true;}
  return checkPassword(key,min,maxBytes);
}
function checkOptionalPair(a,b){
  if(!els[a].value&&!els[b].value){setMsg(b,false,'');mark(b,false);return true;}
  if(!els[b].value){setMsg(b,false,t('startup.reenter_to_confirm'));mark(b,true);return false;}
  return checkPair(a,b);
}

function setStep(id,state){const el=$(id);if(!el)return;el.classList.toggle('is-done',state==='done');el.classList.toggle('is-current',state==='current');}
function updateSteps(recoveryDone,secondaryDone,lockDone,allDone){
  setStep('step-recovery',recoveryDone?'done':'current');
  setStep('step-secondary',recoveryDone?(secondaryDone?'done':'current'):'');
  setStep('step-lock',recoveryDone&&secondaryDone?(lockDone?'done':'current'):'');
  setStep('step-finish',allDone?'current':'');
}

function validate(){
  let ok=true;
  if(isSetup){
    const recoveryOK=checkPassword('password',MIN_REC,MAX_REC_BYTES);
    const recoveryPairOK=checkPair('password','confirm');
    const secondaryOK=checkPassword('secondaryPassword',MIN_SEC,MAX_SEC_BYTES);
    const secondaryPairOK=checkPair('secondaryPassword','secondaryConfirm');
    const lockOK=checkOptionalPassword('lockPassword',MIN_SEC,MAX_SEC_BYTES);
    const lockPairOK=checkOptionalPair('lockPassword','lockConfirm');
    ok=recoveryOK&&recoveryPairOK&&secondaryOK&&secondaryPairOK&&lockOK&&lockPairOK;
    updateSteps(recoveryOK&&recoveryPairOK,secondaryOK&&secondaryPairOK,lockOK&&lockPairOK,ok);
  }else{
    ok=els.password.value.length>0;
  }
  submit.disabled=!ok||!startupSessionReady;
}
Object.values(els).forEach(el=>{if(el)el.addEventListener('input',validate);});
validate();
establishStartupSession();

function renderStartupDone(card, descriptionKey, warningKey) {
  const shell = document.createElement('div');
  shell.className = 'shell';
  const panel = document.createElement('div');
  panel.className = 'done-panel';
  const title = document.createElement('h1');
  title.textContent = t('startup.done_title');
  const description = document.createElement('p');
  description.className = 'desc';
  description.textContent = t(descriptionKey);
  const warning = document.createElement('div');
  warning.className = 'warn';
  const warningTitle = document.createElement('p');
  warningTitle.className = 'warn-title';
  warningTitle.textContent = t(warningKey);
  const copy = document.createElement('p');
  copy.className = 'warn-copy';
  copy.textContent = t('startup.manual_copy');
  warning.append(warningTitle, copy);
  panel.append(title, description, warning);
  shell.append(panel);
  card.replaceChildren(shell);
}

async function submitCredentials(e){
  if(e)e.preventDefault();
  if(submit.disabled)return;
  errBox.textContent='';submit.disabled=true;
  try{
    const res=await fetch('/api/v1/startup/credentials',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:els.password.value,confirm:els.confirm?els.confirm.value:'',secondaryPassword:els.secondaryPassword?els.secondaryPassword.value:'',secondaryConfirm:els.secondaryConfirm?els.secondaryConfirm.value:'',lockPassword:els.lockPassword?els.lockPassword.value:'',lockConfirm:els.lockConfirm?els.lockConfirm.value:''})});
    let data={};try{data=await res.json();}catch(_){data={};}
    if(!res.ok){errBox.textContent=res.status===403?t('startup.session_expired'):(data.error?.message||t('startup.local_request_failed'));submit.disabled=false;return;}
    const card=document.querySelector('.main');
    renderStartupDone(card, 'startup.opening', 'startup.manual_title');
    let polled=0;const poll=async()=>{try{const r=await fetch('/api/v1/startup/status');const d=await r.json();if(d.data?.ready&&d.data.recordURL){location.href=d.data.recordURL;return;}}catch(_){}if(++polled>30){renderStartupDone(card, 'startup.not_opened', 'startup.manual_continue');return;}setTimeout(poll,500);};poll();
  }catch(_){errBox.textContent=t('startup.connect_failed');submit.disabled=false;}
}
document.querySelectorAll('form').forEach(form=>form.addEventListener('submit',submitCredentials));
submit.addEventListener('click',submitCredentials);
