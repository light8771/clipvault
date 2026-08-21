package clipvault

// 恢复页 HTML：语言中立骨架。所有用户可见文案由 recovery.js 在启动时
// 按浏览器语言（zh-CN / en-US）填充（data-i18n 属性），因为恢复服务器
// 运行在数据库无法解锁的场景——此时 i18n 服务不可用，无法走词条查询。
// 文案字典内嵌在 recovery.js 顶部，与词条体系同源维护。
const recoveryHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title data-i18n="title">恢复数据 · ClipVault</title>
<link id="appearanceFavicon" rel="icon" href="/appearance/favicon">
<link rel="stylesheet" href="/recovery.css">
</head>
<body>
<div class="appearance-background" aria-hidden="true">
  <img id="appearanceBackgroundImage" alt="">
  <video id="appearanceBackgroundVideo" muted loop playsinline></video>
</div>
<div id="appearanceOverlay" class="appearance-overlay" aria-hidden="true"></div>
<main class="page">
  <header class="topbar">
    <div class="brand">
      <span class="brand-mark">
        <svg id="recoveryDefaultLogo" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect x="9" y="3" width="6" height="4" rx="1"/><path d="M9 12h6M9 16h4"/></svg>
        <img id="recoveryBrandLogo" hidden alt="">
      </span>
      <span id="recoveryBrandName">ClipVault</span>
    </div>
    <div class="local-status" data-i18n="local_status">仅在本机处理</div>
  </header>

  <section class="shell" aria-labelledby="pageTitle">
    <div class="page-heading">
      <div class="title-row">
        <span class="title-icon">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l3 2"/></svg>
        </span>
        <div>
          <h1 id="pageTitle" data-i18n="page_title">恢复剪贴板数据库</h1>
          <p class="lead" data-i18n="page_lead">当前 Windows 凭据无法解锁数据库。使用之前设置的恢复密码重新绑定，不会改变已有数据。</p>
        </div>
      </div>
    </div>

    <div class="workspace">
      <aside class="intro" data-i18n-aria="steps_aria" aria-label="恢复流程">
        <p class="section-label" data-i18n="steps_label">恢复流程</p>
        <ol class="steps">
          <li data-i18n="step1">输入数据恢复密码</li>
          <li data-i18n="step2">验证数据库密钥</li>
          <li data-i18n="step3">重新绑定到当前 Windows</li>
        </ol>
        <p class="privacy-note">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
          <span data-i18n="privacy_note">恢复密码与数据库内容不会上传。恢复服务仅在本机运行，并会在 15 分钟后关闭。</span>
        </p>
      </aside>

      <div class="panel">
        <h2 class="panel-title" data-i18n="panel_title">输入恢复密码</h2>
        <p class="panel-desc" data-i18n="panel_desc">这不是软件激活码，也不是锁屏密码，而是为数据库单独设置的数据恢复密码。</p>
        <div id="msg" class="message" role="status" aria-live="polite"></div>

        <form id="recoverSection">
          <input type="text" name="username" value="ClipVault data recovery" autocomplete="username" hidden>
          <label class="field-label" for="password" data-i18n="password_label">数据恢复密码</label>
          <div class="input-wrap">
            <input class="field-input" type="password" id="password" name="password" data-i18n-placeholder="password_placeholder" placeholder="请输入数据恢复密码" autocomplete="current-password" autofocus>
            <button class="show-password" id="showPasswordBtn" type="button" aria-controls="password" aria-pressed="false" data-i18n="show">显示</button>
          </div>
          <p class="hint" data-i18n="panel_hint">验证失败不会修改或删除现有数据库。</p>
          <button class="btn btn-primary" id="recoverBtn" type="submit" data-i18n="recover_btn">验证密码并恢复</button>
        </form>

        <details class="alternative" id="newDatabaseDetails">
          <summary data-i18n="alternative_summary">无法找回恢复密码</summary>
          <form class="danger-box" id="newDatabaseForm">
            <p class="danger-title" data-i18n="danger_title">创建新的空数据库</p>
            <p class="danger-copy" data-i18n="danger_copy">原数据库会被隔离保留，但在找回正确密码之前无法读取。新数据库不包含任何历史记录、收藏或设置。</p>
            <label class="field-label" for="confirmText" data-i18n="confirm_label">输入“创建新数据库”确认</label>
            <input class="field-input confirm-input" id="confirmText" type="text" autocomplete="off" data-i18n-placeholder="confirm_phrase" placeholder="创建新数据库">
            <button class="btn btn-danger" id="newBtn" type="submit" disabled data-i18n="new_btn">隔离旧数据并创建新库</button>
          </form>
        </details>

        <p class="finish-note" id="finishNote" data-i18n="finish_note">操作完成后，可以关闭此页面并返回 ClipVault。若创建了新数据库，之后可在“设置 → 存储与诊断 → 隔离的旧数据库”中切回查看旧数据。</p>
      </div>
    </div>
  </section>
</main>
<script src="/recovery.js" defer></script>
</body>
</html>`
