// 设置分组目录：首页搜索与分组页地址共用。
const SETTINGS_GROUPS = [
  { id: 'appearance', title: '外观与通知', desc: '皮肤、显示模式、字体、背景媒体与系统通知' },
  { id: 'general', title: '录制与快捷键', desc: '记录行为、来源应用、快捷键、图片捕获与搜索弹窗' },
  { id: 'security', title: '安全与隐私', desc: '锁屏密码、二次验证与敏感内容过滤' },
  { id: 'storage', title: '存储与数据', desc: '数据目录、隔离数据库与历史清理' },
  { id: 'backup', title: '定时备份', desc: '定时备份到目录或发送到邮箱' },
  { id: 'transfer', title: '导入导出', desc: '导出剪贴板数据，或从导出文件恢复' },
  { id: 'service', title: '网络与服务', desc: 'Web 监听地址与时区' }
];

export function registerSettingsNavigation() {
  registerHomeSearch();
  registerSubNavScrollSpy();
}

/* ── 首页搜索：可搜索分组名称与具体配置项 ─────────────── */

function registerHomeSearch() {
  const input = document.getElementById('settingsSearch');
  const results = document.getElementById('settingsSearchResults');
  const grid = document.getElementById('sgroupGrid');
  if (!input || !results || !grid) return;

  // 配置项索引按需构建：抓取各分组独立页面并解析配置行，保证与页面内容始终一致。
  let indexPromise = null;
  function buildIndex() {
    if (indexPromise) return indexPromise;
    indexPromise = Promise.all(SETTINGS_GROUPS.map(async group => {
      try {
        const html = await fetch('/settings/' + group.id).then(r => r.text());
        const doc = new DOMParser().parseFromString(html, 'text/html');
        const items = [];
        doc.querySelectorAll('.ssec').forEach(section => {
          let fsecTitle = '';
          let fsecID = section.id;
          section.querySelectorAll('.fsec, .field-row, .export-item').forEach(node => {
            if (node.classList.contains('fsec')) {
              const title = node.querySelector('.fsec-title');
              fsecTitle = title ? title.textContent.trim() : '';
              fsecID = node.id || section.id;
              return;
            }
            const labelEl = node.querySelector('.field-label, .export-title');
            if (!labelEl) return;
            const hintEl = node.querySelector('.field-hint, .export-desc');
            items.push({
              label: labelEl.textContent.trim(),
              path: group.title + (fsecTitle ? ' · ' + fsecTitle : ''),
              url: '/settings/' + group.id + '#' + fsecID,
              text: (labelEl.textContent + ' ' + (hintEl ? hintEl.textContent : '')).toLowerCase()
            });
          });
        });
        return items;
      } catch (_) {
        return [];
      }
    })).then(parts => parts.flat());
    return indexPromise;
  }

  function escapeText(value) {
    const span = document.createElement('span');
    span.textContent = value;
    return span.innerHTML;
  }

  async function render() {
    const q = input.value.trim().toLowerCase();
    if (!q) {
      results.classList.add('hidden');
      results.innerHTML = '';
      grid.classList.remove('hidden');
      return;
    }
    const parts = [];
    SETTINGS_GROUPS
      .filter(g => (g.title + ' ' + g.desc).toLowerCase().includes(q))
      .forEach(g => {
        parts.push(
          '<a class="sres-item" href="/settings/' + g.id + '">' +
          '<span class="sres-label">' + escapeText(g.title) + '</span>' +
          '<span class="sres-path">设置分组</span></a>'
        );
      });
    const items = await buildIndex();
    if (q !== input.value.trim().toLowerCase()) return;
    items.filter(item => item.text.includes(q)).slice(0, 24).forEach(item => {
      parts.push(
        '<a class="sres-item" href="' + item.url + '">' +
        '<span class="sres-label">' + escapeText(item.label) + '</span>' +
        '<span class="sres-path">' + escapeText(item.path) + '</span></a>'
      );
    });
    if (!parts.length) {
      parts.push('<p class="sres-empty">没有匹配「' + escapeText(input.value.trim()) + '」的设置。</p>');
    }
    results.innerHTML = parts.join('');
    results.classList.remove('hidden');
    grid.classList.add('hidden');
  }

  input.addEventListener('input', () => { void render(); });
}

/* ── 分组页：组内锚点导航的滚动高亮 ───────────────────── */

function registerSubNavScrollSpy() {
  const anchors = Array.from(document.querySelectorAll('.amenu-anchor'));
  if (!anchors.length) return;
  const pairs = anchors
    .map(a => ({ link: a, target: document.getElementById(a.hash.slice(1)) }))
    .filter(pair => pair.target);
  if (!pairs.length) return;

  let ticking = false;
  function update() {
    ticking = false;
    const mark = window.scrollY + 120;
    let current = pairs[0];
    pairs.forEach(pair => {
      if (pair.target.offsetTop <= mark) current = pair;
    });
    pairs.forEach(pair => {
      const active = pair === current;
      pair.link.classList.toggle('active', active);
      if (active) pair.link.setAttribute('aria-current', 'location');
      else pair.link.removeAttribute('aria-current');
    });
  }
  window.addEventListener('scroll', () => {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(update);
  }, { passive: true });
  update();
}
