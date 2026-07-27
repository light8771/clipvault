# AGENTS.md

## Build & Test

- Go: `C:\Program Files\Go\bin\go.exe` (Go 1.26.3, windows/amd64)
- gofmt: `C:\Program Files\Go\bin\gofmt.exe`
- The `internal/clipvault` package imports DuckDB (`github.com/duckdb/duckdb-go/v2`) via `store.go`.
  DuckDB bindings require **CGO_ENABLED=1** and a C compiler (gcc/mingw).
  In environments without a C compiler, `go build`/`go test` for this package will fail with
  "build constraints exclude all Go files in .../duckdb-go-bindings/lib/windows-amd64".
  This is a pre-existing environment limitation, not a code issue.
- To verify Go syntax without building: `gofmt -e <file.go>` (prints formatted code if syntax is valid).
- To verify JS syntax: `node --check internal/clipvault/web/script.js`
- CSS/HTML have no build step; verify by serving `internal/clipvault/web/` and loading in a browser.

## Frontend Appearance System

- Theme colors are defined as CSS custom properties on `:root` (light) and overridden in
  `@media (prefers-color-scheme: dark)` (dark), then re-set at runtime by `applyTheme()` in
  `internal/clipvault/web/script.js`.
- `--card` is the opaque card color (hex). `--card-rgba` is the rgba version whose alpha is
  controlled by the "界面不透明度" (Surface Opacity) slider (ThemeConfig.SurfaceOpacity, 0–100).
  Used by `article`, `.ssec-fields`, `.export-group`, and `pre::after` gradient.
- `--nav-bg` is the navbar/sidebar background, also derived from the Surface Opacity slider
  (rgba of the bg color). Used by `.navbar` and the sticky `.settings-sidebar`.
- `ThemeConfig.SurfaceOpacity` is a plain `int` (default 100, range 0–100).
  `getConfig` pre-seeds with `DefaultConfig()` before unmarshaling, so absent JSON fields
  keep the default; explicit 0 means "fully transparent".
