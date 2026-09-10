# AGENTS.md

## Build & Test

- Go launcher: `C:\Program Files\Go\bin\go.exe` (GOTOOLCHAIN=auto; module baseline Go 1.26.6, windows/amd64)
- Garble requires the standalone Go SDK outside GOMODCACHE. This workstation has the official `go1.26.6` launcher in the Go bin directory and SDK at `C:\Users\11759\sdk\go1.26.6`; the existing Go launcher selects it automatically. See `signing/README.md` for installation commands.
- gofmt: `C:\Program Files\Go\bin\gofmt.exe`
- MinGW-w64 root: `D:\mingw64`
- GCC: `D:\mingw64\bin\gcc.exe`
- For any Go build/test that reaches DuckDB, use this repository-local command setup:
  `$env:PATH='D:\mingw64\bin;' + $env:PATH; $env:CGO_ENABLED='1'; $env:CC='D:\mingw64\bin\gcc.exe'`
- The `internal/clipvault` package imports DuckDB (`github.com/duckdb/duckdb-go/v2`) via `store.go`.
  DuckDB bindings require **CGO_ENABLED=1** and a C compiler (gcc/mingw).
  This workstation has the required compiler at the path above; use it before concluding that
  CGO is unavailable. In other environments without a C compiler, `go build`/`go test` will fail with
  "build constraints exclude all Go files in .../duckdb-go-bindings/lib/windows-amd64".
- To verify Go syntax without building: `gofmt -e <file.go>` (prints formatted code if syntax is valid).
- To verify JS syntax (run from repo root): `Get-ChildItem -Recurse internal/clipvault/web -Filter *.js | ForEach-Object { node --check $_.FullName }`
- Frontend acceptance: run `npm ci` once to install the locked development-only JavaScript parser, then `./scripts/check-web.ps1` (syntax, HTML/i18n analysis, rendering regressions, and theme checks).
- Order-build regression: `./scripts/test-order-build.ps1` runs the real scripts in a temporary fixture with mocked Go/Garble commands; no production keys or executable are generated.
- Built-in translations live in `internal/clipvault/i18n_builtin.json` and are embedded in the EXE. Keep public translation data out of Go literal tables so Garble does not expand it into expensive executable code. Existing i18n structure/seed tests validate the resource and database initialization.
- CSS/HTML have no build step; verify by serving `internal/clipvault/web/` and loading in a browser.

## Frontend Appearance & Layout System

- Config is split by scope into three top-level sections (see `Config` in `internal/clipvault/config.go`):
  - `AppearanceSettings` (`cfg.appearance`): global visuals only (preset/mode/primary/accent/fontScale/surfaceOpacity).
  - `NavigationSettings` (`cfg.navigation`): navbar behavior (sticky/blur/opacity/divider). Opacity `-1` means "follow surface opacity".
  - `HistoryPageSettings` (`cfg.historyPage`): history-page-only layout (layout `top-bottom`/`left-right`,
    columns 0=auto/1–4, forceColumns, contentWidthMode auto/custom, contentWidth 720–3200,
    horizontalPadding 0–80, cardGap 0–32, previewLines 0–20).
  `Config.UnmarshalJSON` accepts only this current split schema; obsolete `theme` objects are rejected.
- Theme colors are defined as CSS custom properties on `:root` (light) and overridden in
  `@media (prefers-color-scheme: dark)` (dark), then re-set at runtime by `applyTheme()` in
  `internal/clipvault/web/app/appearance-theme.js` (consumes `appearance` + `navigation` only).
- `--card` is the opaque card color (hex). `--card-rgba` is the rgba version whose alpha is
  controlled by the "界面不透明度" (Surface Opacity) slider (AppearanceSettings.SurfaceOpacity, 0–100).
  Used by `article`, `.ssec-fields`, `.export-group`, and `pre::after` gradient.
- `--nav-bg` is the navbar/sidebar background, also derived from the Surface Opacity slider
  (rgba of the bg color). Used by `.navbar` and the sticky `.settings-sidebar`.
- Page containers: all non-history pages share `--page-container-max-width` / `--page-container-padding`
  (tokens.css; 880 → 1120 → 1400px breakpoints). The history page overrides both `#viewHistory` and
  `.nav-inner` with `--history-content-max-width` / `--history-horizontal-padding` so navbar, search,
  filters, cards and pagination share the same container. All history rules are scoped with
  `html[data-app-page="history"]`.
- History column count is CSS-driven (breakpoint ladders in `web/history.css`); JS only writes
  `data-history-columns` / `data-history-columns-force` attributes and the `--history-*` variables
  (`applyHistoryPageLayout()` in `web/app/history-layout.js`). Never reintroduce a JS width→column
  calculator or a per-column fixed page width.
- First paint: `serveApplicationPage` emits `data-app-page` and history layout attributes on `<html>`,
  and `/theme-boot.css` (themeBootCSS) emits all CSS variables incl. `--history-*` to avoid FOUC.
- Maintenance/support contact info is app metadata (not user settings):
  `currentApplicationMetadata()` in `application_meta.go`, exposed read-only via
  `GET /api/v1/system/info` (`support` field); empty entries are hidden in the UI.
