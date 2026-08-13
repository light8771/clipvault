# ClipVault browser smoke

Run the isolated preview with the repository CGO environment and a bounded
`-lifetime` (for example `-lifetime 10m`), then drive the printed URL with the
Playwright CLI. Store screenshots and traces only under `output/playwright/`.
Set `CLIPVAULT_PREVIEW_SECONDARY_PASSWORD` to a strong disposable password when
the smoke run needs to exercise secondary-authentication expiry and retry. The
preview stores only its hash in the temporary data directory and removes that
directory at shutdown.

Before launching the browser, run `scripts/check-web.ps1`. Every application
feature under `web/app/` is an independently parsed native ES module loaded from
the `/app/bootstrap.js` module graph. The script checks each module plus the
standalone `api.js`, `store.js`, `startup.js`, and `recovery.js` modules.

Required checks:

1. Open the history page at 1440px and verify no console errors or failed JS/CSS resources.
2. Switch between history and settings; visit every settings sidebar section.
3. Exercise source application, password, recovery, export, import, backup history, and group dialogs; verify open, focus, cancel, and close behavior.
4. Repeat responsive snapshots at 1024px, 768px, and 390px; verify no horizontal page overflow and that primary actions remain reachable.
5. Emulate dark color scheme and reduced motion; verify theme contrast and usable focus indicators.
6. Confirm unknown legacy `/api/*` URLs return 404 while `/api/v1/session` returns the API envelope.
7. Stop the preview and verify its temporary data directory is removed.

For first-start and recovery-gate automation, set
`CLIPVAULT_BROWSER_URL_FILE` to an ignored file under `output/playwright/`.
ClipVault writes the one-time local URL there instead of opening the system
browser. Treat the file as a short-lived secret and remove it after the run.
