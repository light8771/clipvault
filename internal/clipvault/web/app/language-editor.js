import { app } from './app-context.js';

// 语言管理页：当前语言切换、官方/自定义语言分区展示、新建/删除；
// 翻译编辑视图：搜索、状态/模块筛选、分页、失焦自动保存（空白即删除）。
// 变更类操作（新建/删除/保存译文）经 protectedFetch 走二次认证。
export function registerLanguageEditor() {
  const root = document.getElementById('langPage');
  if (!root) return;

  const t = (key, params) => window.clipvaultI18n.t(key, params);

  // ── 列表视图 ──
  const elCurrentSelect = document.getElementById('langCurrentSelect');
  const elBuiltinList = document.getElementById('langBuiltinList');
  const elCustomList = document.getElementById('langCustomList');
  const elNewForm = document.getElementById('langNewForm');
  const elNewToggle = document.getElementById('langNewToggle');
  const elNewCancel = document.getElementById('langNewCancel');
  const elNewCode = document.getElementById('langNewCode');
  const elNewName = document.getElementById('langNewName');
  const elNewNative = document.getElementById('langNewNative');
  const elNewBase = document.getElementById('langNewBase');
  const elNewError = document.getElementById('langNewError');

  // ── 编辑视图 ──
  const elListView = document.getElementById('langListView');
  const elEditorView = document.getElementById('langEditorView');
  const elEditorTitle = document.getElementById('langEditorTitle');
  const elEditorBaseNote = document.getElementById('langEditorBaseNote');
  const elEditorCompletion = document.getElementById('langEditorCompletion');
  const elEditorBack = document.getElementById('langEditorBack');
  const elEditorSearch = document.getElementById('langEditorSearch');
  const elEditorModule = document.getElementById('langEditorModule');
  const elEditorBody = document.getElementById('langEditorBody');
  const elEditorReadonly = document.getElementById('langEditorReadonly');
  const elPagerPrev = document.getElementById('langPagerPrev');
  const elPagerNext = document.getElementById('langPagerNext');
  const elPagerInfo = document.getElementById('langPagerInfo');

  // 语言显示名缓存（从 API 的 languages 数据构建，避免硬编码文案）。
  const languageNames = {};
  const editorState = { code: '', canEdit: true, page: 1, pageSize: 100, total: 0, status: '', module: '', query: '', pending: new Set() };
  let searchTimer = 0;

  // ─── 数据加载 ───
  async function loadLanguages() {
    try {
      const d = await app.fetch('/api/v1/i18n/languages').then(r => r.json());
      renderLanguages(d);
    } catch (_) {}
  }

  function renderLanguages(d) {
    const languages = d.languages || [];
    const configured = d.configuredValue || 'auto';
    // 当前语言下拉：跟随系统 + 全部启用语言。
    elCurrentSelect.textContent = '';
    const autoOption = document.createElement('option');
    autoOption.value = 'auto';
    autoOption.textContent = t('i18n.follow_system');
    elCurrentSelect.appendChild(autoOption);
    for (const lang of languages) {
      if (!lang.isEnabled) continue;
      const option = document.createElement('option');
      option.value = lang.code;
      option.textContent = (lang.nativeName || lang.name) + ' · ' + lang.code;
      if (lang.code === configured) option.selected = true;
      elCurrentSelect.appendChild(option);
      languageNames[lang.code] = (lang.name + (lang.nativeName && lang.nativeName !== lang.name ? ' / ' + lang.nativeName : ''));
    }

    // 官方 / 自定义分区。
    elBuiltinList.textContent = '';
    elCustomList.textContent = '';
    let customCount = 0;
    for (const lang of languages) {
      const row = buildLanguageRow(lang);
      if (lang.isBuiltin) elBuiltinList.appendChild(row);
      else { elCustomList.appendChild(row); customCount++; }
    }
    if (!customCount) {
      const empty = document.createElement('li');
      empty.className = 'lang-row lang-row-empty';
      const span = document.createElement('span');
      span.className = 'lang-row-native';
      span.textContent = t('i18n.empty_custom');
      empty.appendChild(span);
      elCustomList.appendChild(empty);
    }
  }

  function buildLanguageRow(lang) {
    const row = document.createElement('li');
    row.className = 'lang-row';

    const main = document.createElement('div');
    main.className = 'lang-row-main';
    const name = document.createElement('span');
    name.className = 'lang-row-name';
    name.textContent = lang.name;
    const code = document.createElement('span');
    code.className = 'lang-row-code';
    code.textContent = lang.code;
    main.appendChild(name);
    main.appendChild(code);
    if (lang.nativeName && lang.nativeName !== lang.name) {
      const native = document.createElement('span');
      native.className = 'lang-row-native';
      native.textContent = lang.nativeName;
      main.appendChild(native);
    }
    row.appendChild(main);

    const badge = document.createElement('span');
    badge.className = 'lang-badge' + (lang.isBuiltin ? '' : ' lang-badge-custom');
    badge.textContent = t(lang.isBuiltin ? 'i18n.builtin_badge' : 'i18n.custom_badge');
    row.appendChild(badge);

    // 完成度：细进度条 + n/m 已翻译。
    const completion = document.createElement('div');
    completion.className = 'lang-completion';
    const bar = document.createElement('div');
    bar.className = 'lang-completion-bar';
    const fill = document.createElement('div');
    fill.className = 'lang-completion-fill';
    const total = Number(lang.total) || 0;
    const translated = Math.min(Number(lang.translated) || 0, total);
    fill.style.width = (total > 0 ? Math.round((translated / total) * 100) : 0) + '%';
    bar.appendChild(fill);
    const label = document.createElement('span');
    label.className = 'lang-completion-label';
    label.textContent = t('i18n.completion_label', { translated: translated, total: total });
    completion.appendChild(bar);
    completion.appendChild(label);
    row.appendChild(completion);

    const actions = document.createElement('div');
    actions.className = 'lang-row-actions';
    const editBtn = document.createElement('button');
    editBtn.type = 'button';
    editBtn.className = 'btn';
    editBtn.textContent = t('common.edit');
    editBtn.addEventListener('click', () => openEditor(lang.code));
    actions.appendChild(editBtn);
    if (!lang.isBuiltin) {
      const deleteBtn = document.createElement('button');
      deleteBtn.type = 'button';
      deleteBtn.className = 'btn lang-btn-danger';
      deleteBtn.textContent = t('common.delete');
      // 两段式确认：第一次点击变为确认文案，2.5 秒内未点击恢复。
      let armed = false;
      let timer = 0;
      deleteBtn.addEventListener('click', async () => {
        if (!armed) {
          armed = true;
          deleteBtn.textContent = t('common.confirm');
          timer = window.setTimeout(() => {
            armed = false;
            deleteBtn.textContent = t('common.delete');
          }, 2500);
          return;
        }
        window.clearTimeout(timer);
        armed = false;
        deleteBtn.textContent = t('common.delete');
        if (!window.confirm(t('i18n.delete_confirm'))) return;
        try {
          const res = await app.protectedFetch('/api/v1/i18n/languages?code=' + encodeURIComponent(lang.code), { method: 'DELETE' });
          if (!res.ok) {
            const failure = await app.readAPIError(res);
            app.toast(failure.message, 'err');
            return;
          }
          app.toast(t('common.delete') + ' ✓');
          await loadLanguages();
        } catch (err) {
          app.toast(String(err && err.message || err), 'err');
        }
      });
      actions.appendChild(deleteBtn);
    }
    row.appendChild(actions);
    return row;
  }

  // ─── 当前语言切换 ───
  elCurrentSelect.addEventListener('change', async () => {
    const code = elCurrentSelect.value;
    try {
      const res = await app.fetch('/api/v1/i18n/current-language', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ code: code })
      });
      if (!res.ok) {
        const failure = await app.readAPIError(res);
        app.toast(failure.message, 'err');
        await loadLanguages();
        return;
      }
      // 语言切换经整页刷新生效（SSR 模板 + dict.js 均为 no-cache）。
      window.location.reload();
    } catch (err) {
      app.toast(String(err && err.message || err), 'err');
    }
  });

  // ─── 新建语言 ───
  elNewToggle.addEventListener('click', () => {
    elNewForm.hidden = !elNewForm.hidden;
    if (!elNewForm.hidden) elNewCode.focus();
  });
  elNewCancel.addEventListener('click', () => {
    elNewForm.hidden = true;
    elNewError.hidden = true;
    elNewForm.reset();
  });
  elNewForm.addEventListener('submit', async event => {
    event.preventDefault();
    elNewError.hidden = true;
    const payload = {
      code: elNewCode.value.trim(),
      name: elNewName.value.trim(),
      nativeName: elNewNative.value.trim(),
      baseLanguageCode: elNewBase.value
    };
    if (!payload.code || !payload.name) {
      elNewError.textContent = t('i18n.error.name_invalid');
      elNewError.hidden = false;
      return;
    }
    try {
      const res = await app.protectedFetch('/api/v1/i18n/languages', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!res.ok) {
        const failure = await app.readAPIError(res);
        elNewError.textContent = failure.message;
        elNewError.hidden = false;
        return;
      }
      elNewForm.hidden = true;
      elNewForm.reset();
      app.toast(t('i18n.create') + ' ✓');
      await loadLanguages();
    } catch (err) {
      elNewError.textContent = String(err && err.message || err);
      elNewError.hidden = false;
    }
  });

  // ─── 翻译编辑视图 ───
  function openEditor(code) {
    editorState.code = code;
    editorState.page = 1;
    editorState.status = '';
    editorState.module = '';
    editorState.query = '';
    elEditorSearch.value = '';
    for (const btn of document.querySelectorAll('#langEditorFilters .lang-seg-btn')) {
      btn.classList.toggle('active', btn.dataset.status === '');
    }
    elListView.hidden = true;
    elEditorView.hidden = false;
    loadEntries();
  }

  elEditorBack.addEventListener('click', () => {
    elEditorView.hidden = true;
    elListView.hidden = false;
    loadLanguages();
  });

  for (const btn of document.querySelectorAll('#langEditorFilters .lang-seg-btn')) {
    btn.addEventListener('click', () => {
      for (const other of document.querySelectorAll('#langEditorFilters .lang-seg-btn')) other.classList.remove('active');
      btn.classList.add('active');
      editorState.status = btn.dataset.status || '';
      editorState.page = 1;
      loadEntries();
    });
  }

  elEditorModule.addEventListener('change', () => {
    editorState.module = elEditorModule.value;
    editorState.page = 1;
    loadEntries();
  });

  elEditorSearch.addEventListener('input', () => {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(() => {
      editorState.query = elEditorSearch.value.trim();
      editorState.page = 1;
      loadEntries();
    }, 250);
  });

  elPagerPrev.addEventListener('click', () => {
    if (editorState.page > 1) { editorState.page--; loadEntries(); }
  });
  elPagerNext.addEventListener('click', () => {
    if (editorState.page * editorState.pageSize < editorState.total) { editorState.page++; loadEntries(); }
  });

  async function loadEntries() {
    const params = new URLSearchParams({
      lang: editorState.code,
      page: String(editorState.page),
      pageSize: String(editorState.pageSize)
    });
    if (editorState.query) params.set('q', editorState.query);
    if (editorState.module) params.set('module', editorState.module);
    if (editorState.status) params.set('status', editorState.status);
    let d;
    try {
      d = await app.fetch('/api/v1/i18n/entries?' + params.toString()).then(r => r.json());
    } catch (_) {
      return;
    }
    renderEntries(d);
  }

  function renderEntries(d) {
    const lang = d.language || {};
    editorState.canEdit = !!d.canEdit;
    editorState.total = Number(d.total) || 0;
    elEditorTitle.textContent = (lang.nativeName || lang.name || '') + ' · ' + (lang.code || '');
    elEditorBaseNote.textContent = t('i18n.editor.base_note', { name: d.baseName || d.baseCode || '-' });
    const counts = d.counts || {};
    const total = Number(counts.total) || 0;
    const translated = Math.min(Number(counts.translated) || 0, total);
    elEditorCompletion.textContent = t('i18n.editor.completion_note', {
      percent: total > 0 ? Math.round((translated / total) * 100) : 0
    }) + ' · ' + t('i18n.completion_label', { translated: translated, total: total });
    elEditorReadonly.hidden = editorState.canEdit;

    // 模块下拉：保留当前选择。
    const previousModule = editorState.module;
    elEditorModule.textContent = '';
    const allOption = document.createElement('option');
    allOption.value = '';
    allOption.textContent = t('i18n.editor.module_all');
    elEditorModule.appendChild(allOption);
    for (const module of d.modules || []) {
      const option = document.createElement('option');
      option.value = module;
      option.textContent = module;
      if (module === previousModule) option.selected = true;
      elEditorModule.appendChild(option);
    }
    editorState.module = elEditorModule.value;

    // 词条表。
    elEditorBody.textContent = '';
    for (const entry of d.entries || []) {
      elEditorBody.appendChild(buildEntryRow(entry));
    }
    if (!d.entries || !d.entries.length) {
      const empty = document.createElement('div');
      empty.className = 'lang-entry lang-entry-empty';
      const span = document.createElement('span');
      span.className = 'lang-row-native';
      span.textContent = t('common.none');
      empty.appendChild(span);
      elEditorBody.appendChild(empty);
    }

    // 分页。
    const totalPages = Math.max(1, Math.ceil(editorState.total / editorState.pageSize));
    elPagerPrev.disabled = editorState.page <= 1;
    elPagerNext.disabled = editorState.page >= totalPages;
    elPagerInfo.textContent = t('i18n.editor.page_info', { page: editorState.page, totalPages: totalPages });
  }

  function buildEntryRow(entry) {
    const row = document.createElement('div');
    row.className = 'lang-entry' + (entry.customText ? '' : ' lang-entry-untranslated');

    const source = document.createElement('div');
    source.className = 'lang-col-source';
    const sourceText = document.createElement('span');
    sourceText.className = 'lang-source-text';
    sourceText.textContent = entry.sourceText || '—';
    const sourceKey = document.createElement('span');
    sourceKey.className = 'lang-source-key';
    sourceKey.textContent = entry.key;
    source.appendChild(sourceText);
    source.appendChild(sourceKey);
    row.appendChild(source);

    const translation = document.createElement('div');
    translation.className = 'lang-col-translation';
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'lang-entry-input' + (entry.customText ? '' : ' lang-untranslated');
    input.value = entry.customText || '';
    input.placeholder = editorState.canEdit ? t('i18n.editor.untranslated_hint') : '—';
    input.disabled = !editorState.canEdit;
    input.autocomplete = 'off';
    const status = document.createElement('span');
    status.className = 'lang-entry-status';
    translation.appendChild(input);
    translation.appendChild(status);
    row.appendChild(translation);

    if (editorState.canEdit) {
      const initialValue = entry.customText || '';
      let saveTimer = 0;
      const showStatus = (kind) => {
        status.className = 'lang-entry-status ' + (kind === 'ok' ? 'is-saved' : 'is-error');
        status.textContent = kind === 'ok' ? '✓' : '!';
        window.clearTimeout(saveTimer);
        saveTimer = window.setTimeout(() => {
          status.className = 'lang-entry-status';
          status.textContent = '';
        }, 1800);
      };
      // 失焦保存：内容变化才提交；空白 = 删除（空翻译语义）。
      input.addEventListener('blur', async () => {
        const value = input.value.trim();
        if (value === initialValue) return;
        const key = entry.key;
        if (editorState.pending.has(key)) return;
        editorState.pending.add(key);
        try {
          const res = await app.protectedFetch('/api/v1/i18n/entries?lang=' + encodeURIComponent(editorState.code) + '&key=' + encodeURIComponent(key), {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ value: value })
          });
          if (!res.ok) {
            const failure = await app.readAPIError(res);
            app.toast(failure.message, 'err');
            showStatus('err');
            input.value = initialValue;
            return;
          }
          entry.customText = value;
          row.classList.toggle('lang-entry-untranslated', !value);
          input.classList.toggle('lang-untranslated', !value);
          showStatus('ok');
        } catch (err) {
          app.toast(String(err && err.message || err), 'err');
          showStatus('err');
          input.value = initialValue;
        } finally {
          editorState.pending.delete(key);
        }
      });
    }
    return row;
  }

  // 页面进入时加载（服务端模板保证 langPage 只在语言页渲染）。
  loadLanguages();
}
