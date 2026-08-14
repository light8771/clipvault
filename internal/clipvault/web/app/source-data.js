import { app } from './app-context.js';

export function registerSourceData() {
  app.sourceAliases = {};
  app.sourceWhitelist = null;
  app.sourceAppsDraft = null;
  app.allSourceExes = [];
  app.currentSourceExes = [];
  app.loadSourceExes = async function loadSourceExes() {
    try {
      const data = await app.fetch('/api/v1/items/sources').then(response => response.json());
      app.allSourceExes = Array.isArray(data) ? data : [];
    } catch (_) {
      app.allSourceExes = [];
    }
    app.renderSrcExeDropdown?.();
  };
}
