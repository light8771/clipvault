# ClipVault browser smoke

Run the isolated preview with the repository CGO environment and a bounded
`-lifetime` (for example `-lifetime 10m`), then drive the printed URL with the
Playwright CLI. Store screenshots and traces only under `output/playwright/`.

Before launching the browser, run `scripts/check-web.ps1`. The application
feature sources under `web/app/` are ordered fragments of the single native ES
module served at `/script.js`; the script checks that composed module plus the
standalone `api.js` and `store.js` modules.

Required checks:

1. Open the history page at 1440px and verify no console errors or failed JS/CSS resources.
2. Switch between history and settings; visit every settings sidebar section.
3. Exercise source application, password, recovery, export, import, backup history, and group dialogs; verify open, focus, cancel, and close behavior.
4. Repeat responsive snapshots at 1024px, 768px, and 390px; verify no horizontal page overflow and that primary actions remain reachable.
5. Emulate dark color scheme and reduced motion; verify theme contrast and usable focus indicators.
6. Confirm unknown legacy `/api/*` URLs return 404 while `/api/v1/session` returns the API envelope.
7. Stop the preview and verify its temporary data directory is removed.
