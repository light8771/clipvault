// 设置分组目录：首页搜索与分组页地址共用。文案复用官方词条，
// 与设置首页卡片的展示保持一致。
const t = window.clipvaultI18n.t;

const SETTINGS_GROUPS = [
  { id: 'appearance', titleKey: 'app.title.appearance', descKey: 'settings_home.appearance_desc' },
  { id: 'history', titleKey: 'app.title.settings_history', descKey: 'settings_home.history_desc' },
  { id: 'general', titleKey: 'app.title.general', descKey: 'settings_home.general_desc' },
  { id: 'security', titleKey: 'app.title.security', descKey: 'settings_home.security_desc' },
  { id: 'storage', titleKey: 'app.title.storage', descKey: 'settings_home.storage_desc' },
  { id: 'backup', titleKey: 'app.title.backup', descKey: 'settings_home.backup_desc' },
  { id: 'transfer', titleKey: 'app.title.transfer', descKey: 'settings_home.transfer_desc' },
  { id: 'service', titleKey: 'app.title.service', descKey: 'settings_home.service_desc' }
].map(group => ({ ...group, title: t(group.titleKey), desc: t(group.descKey) }));

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
          '<span class="sres-path">' + escapeText(t('settings_home.group_path')) + '</span></a>'
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
      parts.push('<p class="sres-empty">' + escapeText(t('settings_home.no_match', { query: input.value.trim() })) + '</p>');
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
