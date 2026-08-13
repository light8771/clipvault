import { app } from "./app-context.js";
export function registerSettingsSources() {
  app.updateSourceAppsSummary = function updateSourceAppsSummary() {
    const summary = document.getElementById('sourceAppsSummary');
    if (!summary) return;
    const aliasCount = Object.keys(app.sourceAliases || {}).length;
    const scope = app.sourceWhitelist?.length ? `下拉显示 ${app.sourceWhitelist.length} 个应用` : '下拉显示全部应用';
    summary.textContent = `${scope}；已设置 ${aliasCount} 个别名`;
  };
  app.renderSourceAppsDialog = function renderSourceAppsDialog() {
    const wrap = document.getElementById('srcAliases');
    if (!wrap || !app.sourceAppsDraft) return;
    if (!app.allSourceExes.length) {
      wrap.innerHTML = '<p class="src-empty-hint">暂无已记录的来源应用</p>';
      app.updateSourceAppsSelectionHint();
      return;
    }
    const query = document.getElementById('sourceAppsFilter')?.value.trim().toLowerCase() || '';
    const visible = app.allSourceExes.filter(e => {
      const alias = app.sourceAppsDraft.aliases[e.name] || '';
      return !query || e.name.toLowerCase().includes(query) || alias.toLowerCase().includes(query);
    });
    const selectedMode = app.sourceAppsDraft.mode === 'selected';
    const selected = new Set(selectedMode ? app.sourceAppsDraft.whitelist || [] : app.allSourceExes.map(e => e.name));
    wrap.innerHTML = visible.map(e => {
      const custom = app.sourceAppsDraft.aliases[e.name] || '';
      return `<div class="src-alias-row" data-raw="${app.esc(e.name)}">
        <label class="src-app-check" title="控制是否出现在搜索下拉"><input type="checkbox"${selected.has(e.name) ? ' checked' : ''}${selectedMode ? '' : ' disabled'} aria-label="${app.esc(e.name)} 出现在搜索下拉"></label>
        <span class="src-alias-raw" title="${app.esc(e.name)}">${app.esc(e.name)}</span>
        <input type="text" class="src-alias-input" maxlength="40" placeholder="别名（可选）" value="${app.esc(custom)}">
      </div>`;
    }).join('');
    if (!visible.length) wrap.innerHTML = '<p class="src-empty-hint">没有匹配的来源应用</p>';
    app.updateSourceAppsSelectionHint();
  };
  app.updateSourceAppsSelectionHint = function updateSourceAppsSelectionHint() {
    const hint = document.getElementById('sourceAppsSelectionHint');
    if (!hint || !app.sourceAppsDraft) return;
    if (app.sourceAppsDraft.mode === 'all') {
      hint.textContent = `共 ${app.allSourceExes.length} 个应用，全部显示在搜索下拉`;
      return;
    }
    const knownNames = new Set(app.allSourceExes.map(e => e.name));
    const configured = app.sourceAppsDraft.whitelist || [];
    const selectedKnown = configured.filter(name => knownNames.has(name)).length;
    const unavailable = configured.length - selectedKnown;
    hint.textContent = `已选择 ${selectedKnown} / ${app.allSourceExes.length} 个当前应用` + (unavailable ? `；另保留 ${unavailable} 个历史应用` : '');
  };
  app.syncVisibleSourceAppsDraft = function syncVisibleSourceAppsDraft() {
    if (!app.sourceAppsDraft) return;
    document.querySelectorAll('#srcAliases .src-alias-row').forEach(row => {
      const raw = row.dataset.raw;
      const input = row.querySelector('.src-alias-input');
      const checkbox = row.querySelector('.src-app-check input');
      if (!raw) return;
      const alias = input?.value.trim() || '';
      if (alias) app.sourceAppsDraft.aliases[raw] = alias;else delete app.sourceAppsDraft.aliases[raw];
      if (app.sourceAppsDraft.mode === 'selected' && checkbox) {
        const selected = new Set(app.sourceAppsDraft.whitelist || []);
        if (checkbox.checked) selected.add(raw);else selected.delete(raw);
        const knownNames = new Set(app.allSourceExes.map(e => e.name));
        const unavailableConfigured = (app.sourceAppsDraft.whitelist || []).filter(name => !knownNames.has(name));
        app.sourceAppsDraft.whitelist = app.allSourceExes.map(e => e.name).filter(name => selected.has(name)).concat(unavailableConfigured);
      }
    });
  };
  app.openSourceAppsDialog = function () {
    const existing = app.sourceWhitelist?.length ? app.sourceWhitelist.slice() : app.allSourceExes.map(e => e.name);
    app.sourceAppsDraft = {
      aliases: {
        ...app.sourceAliases
      },
      whitelist: existing,
      mode: app.sourceWhitelist?.length ? 'selected' : 'all'
    };
    const filter = document.getElementById('sourceAppsFilter');
    if (filter) filter.value = '';
    document.querySelectorAll('input[name="sourceAppsMode"]').forEach(radio => {
      radio.checked = radio.value === app.sourceAppsDraft.mode;
    });
    app.renderSourceAppsDialog();
    document.getElementById('sourceAppsModal')?.classList.remove('hidden');
    setTimeout(() => filter?.focus(), 50);
  };
  app.closeSourceAppsDialog = function () {
    document.getElementById('sourceAppsModal')?.classList.add('hidden');
    app.sourceAppsDraft = null;
  };
  app.saveSourceAppsDialog = async function () {
    if (!app.sourceAppsDraft) return;
    app.syncVisibleSourceAppsDraft();
    const knownNames = new Set(app.allSourceExes.map(e => e.name));
    const selectedKnown = (app.sourceAppsDraft.whitelist || []).filter(name => knownNames.has(name));
    if (app.sourceAppsDraft.mode === 'selected' && selectedKnown.length === 0) {
      app.toast('“仅显示选中”模式下请至少选择一个应用', 'err');
      return;
    }
    const previousAliases = app.sourceAliases;
    const previousWhitelist = app.sourceWhitelist;
    app.sourceAliases = {
      ...app.sourceAppsDraft.aliases
    };
    app.sourceWhitelist = app.sourceAppsDraft.mode === 'all' ? null : app.sourceAppsDraft.whitelist.slice();
    const saveButton = document.getElementById('sourceAppsSaveBtn');
    if (saveButton) saveButton.disabled = true;
    app.pendingConfigRevision++;
    clearTimeout(app.saveTimer);
    app.saveTimer = 0;
    const saved = await app.saveConfig();
    if (!saved) {
      app.sourceAliases = previousAliases;
      app.sourceWhitelist = previousWhitelist;
      if (saveButton) saveButton.disabled = false;
      return;
    }
    app.updateSourceAppsSummary();
    app.renderSrcExeDropdown();
    app.closeSourceAppsDialog();
  };
  document.getElementById('sourceAppsFilter')?.addEventListener('input', () => {
    app.syncVisibleSourceAppsDraft();
    app.renderSourceAppsDialog();
  });
  document.querySelectorAll('input[name="sourceAppsMode"]').forEach(radio => {
    radio.addEventListener('change', () => {
      if (!app.sourceAppsDraft || !radio.checked) return;
      app.syncVisibleSourceAppsDraft();
      app.sourceAppsDraft.mode = radio.value;
      if (radio.value === 'selected' && !app.sourceAppsDraft.whitelist?.length) {
        app.sourceAppsDraft.whitelist = app.allSourceExes.map(e => e.name);
      }
      app.renderSourceAppsDialog();
    });
  });
  document.getElementById('srcAliases')?.addEventListener('change', () => {
    app.syncVisibleSourceAppsDraft();
    app.updateSourceAppsSelectionHint();
  });

  // ── Unlock filter rules (secondary auth) ────────────────
  app.unlockFilterBtn = document.getElementById('unlockFilterBtn');
  if (app.unlockFilterBtn) {
    app.unlockFilterBtn.addEventListener('click', async () => {
      try {
        const d = await app.protectedFetch('/api/v1/config/sensitive-rules').then(r => r.json());
        const patterns = d.customPatterns || [];
        document.getElementById('filterAuthGate').classList.add('hidden');
        document.getElementById('filterContent').classList.remove('hidden');
        app.renderPatterns(patterns);
        app.toast('已解锁敏感过滤规则');
      } catch (_) {
        app.toast('加载过滤规则失败', 'err');
      }
    });
  }
  // ── Pattern list ──────────────────────────────────────────
  app.renderPatterns = function renderPatterns(patterns) {
    const list = document.getElementById('patternList');
    if (!patterns.length) {
      list.innerHTML = '<p class="no-patterns">暂无过滤规则，所有内容均会被记录</p>';
      return;
    }
    list.innerHTML = patterns.map((p, i) => `
      <div class="pattern-row" data-idx="${i}">
        <input type="text" class="pattern-input" value="${app.esc(p)}" placeholder="正则表达式">
        <button class="pattern-del" data-action="remove-pattern" data-index="${i}" title="删除">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
    `).join('');
  };
  app.removePattern = function (idx) {
    const patterns = app.getPatterns();
    patterns.splice(idx, 1);
    app.renderPatterns(patterns);
    app.scheduleConfigSave(300);
  };
  app.getPatterns = function getPatterns() {
    return Array.from(document.querySelectorAll('.pattern-input')).map(i => i.value.trim()).filter(Boolean);
  };
  document.getElementById('addPatternBtn').addEventListener('click', () => {
    const patterns = app.getPatterns();
    patterns.push('');
    app.renderPatterns(patterns);
    // 聚焦最后一个输入框
    const inputs = document.querySelectorAll('.pattern-input');
    if (inputs.length) inputs[inputs.length - 1].focus();
  });

  // ── Auto-save settings ───────────────────────────────────
}
