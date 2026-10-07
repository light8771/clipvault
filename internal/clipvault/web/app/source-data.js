import { app } from './app-context.js';

const SOURCE_LOAD_OK = 'ok';
const SOURCE_LOAD_FAILED = 'failed';
const SOURCE_LOAD_STALE = 'stale';

export function registerSourceData() {
  app.sourceAliases = {};
  app.sourceWhitelist = null;
  app.sourceAppsDraft = null;
  app.allSourceExes = [];
  app.currentSourceExes = [];
  app.sourceStatsRecordType = '';
  app.sourceStatsSensitiveAccess = false;
  app.sourceStatsRevision = 0;
  app.invalidateSourceExes = function invalidateSourceExes() {
    app.sourceStatsRevision++;
    app.allSourceExes = [];
    app.currentSourceExes = [];
    app.sourceStatsRecordType = '';
    app.sourceStatsSensitiveAccess = false;
    app.renderSrcExeDropdown?.();
  };
  app.loadSourceExes = async function loadSourceExes(recordType = 'content') {
    const sensitiveAccess = !!app.authState?.secondaryAuthed;
    if (app.sourceStatsSensitiveAccess && !sensitiveAccess) {
      // Remove privileged names before waiting for a potentially slow refresh.
      app.invalidateSourceExes();
    }
    const revision = ++app.sourceStatsRevision;
    let sourceExes;
    try {
      const params = new URLSearchParams({ record_type: recordType });
      const response = await app.fetch('/api/v1/items/sources?' + params);
      const data = await response.json();
      if (response.ok === false || !Array.isArray(data)) {
        return revision === app.sourceStatsRevision && sensitiveAccess === !!app.authState?.secondaryAuthed
          ? SOURCE_LOAD_FAILED : SOURCE_LOAD_STALE;
      }
      sourceExes = data;
    } catch (_) {
      return revision === app.sourceStatsRevision && sensitiveAccess === !!app.authState?.secondaryAuthed
        ? SOURCE_LOAD_FAILED : SOURCE_LOAD_STALE;
    }
    if (revision !== app.sourceStatsRevision || sensitiveAccess !== !!app.authState?.secondaryAuthed) return SOURCE_LOAD_STALE;
    app.allSourceExes = sourceExes;
    app.sourceStatsRecordType = recordType;
    app.sourceStatsSensitiveAccess = sensitiveAccess;
    app.renderSrcExeDropdown?.();
    return SOURCE_LOAD_OK;
  };
  app.loadSourceExesForHistory = async function loadSourceExesForHistory(recordType, { refresh = false } = {}) {
    const sensitiveAccess = !!app.authState?.secondaryAuthed;
    if (!refresh && app.sourceStatsRecordType === recordType && app.sourceStatsSensitiveAccess === sensitiveAccess) {
      // Returning to the already-cached projection also supersedes any older
      // in-flight request for a different projection or visibility boundary.
      app.sourceStatsRevision++;
      return true;
    }
    const result = await app.loadSourceExes(recordType);
    if (result === SOURCE_LOAD_STALE) return false;
    if (result === SOURCE_LOAD_OK) {
      const available = new Set(app.allSourceExes.map(item => item.name));
      app.currentSourceExes = app.currentSourceExes.filter(name => available.has(name));
      app.renderSrcExeDropdown();
    }
    return true;
  };
}
