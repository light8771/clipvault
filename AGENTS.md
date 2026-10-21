# AGENTS.md

> **New to this repo?** Read `docs/ai-handoff/HANDOFF.md` first. It holds the project brief,
> architecture decisions, verified history, user hard constraints, and the current top-3 priorities
> distilled from 90 prior sessions. This file covers mechanics; that file covers intent.
> 交接资料是本地私人上下文，不进入公开源码快照；公开副本没有该文件时，先读 README、CONTRIBUTING 和 docs/OSS_RELEASE_REPORT.md。

## Build & Test

- Go launcher: `C:\Program Files\Go\bin\go.exe` (GOTOOLCHAIN=auto; module baseline Go 1.26.10, windows/amd64)
- Garble requires the standalone Go SDK outside GOMODCACHE. This workstation has the official `go1.26.10` launcher in the Go bin directory and SDK at `%USERPROFILE%\sdk\go1.26.10`; the existing Go launcher selects it automatically. See `signing/README.md` for installation commands.
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

## Hard Constraints

These are enforced by tests or by the build. Violating them breaks CI-equivalent acceptance, not just style.

- **Modularity gates** (`internal/clipvault/architecture_test.go:TestProductionFilesStayModular`):
  production files must stay under **Go 700 / JS 500 / CSS 800 / HTML 800** lines.
  When a file crosses the limit, **split it into new modules**. Never raise the threshold and never
  compress code to fit; both have been explicitly rejected.
- **Dependency direction** is enforced by the same file (`TestCorePackagesHaveNoTransportPlatformOrStorageBackEdges`,
  `TestArchitectureDependencyDirection`). `domain` and `application` must not import
  `transport`/`platform`/`storage`/`security`. Layering is proven by the compiler, not by convention.
- **Frontend modules must be registered in `server_static.go`** when added. `node --check` and the
  dependency-graph test will pass while the real browser 404s; this has bitten twice
  (`history-load-error.js`, the pagination module).
- **Frontend module assets match embedded files** is asserted by `TestApplicationModuleAssetsMatchEmbeddedFiles`.

## Zero-Compatibility Rule

This project has no legacy compatibility and no migration paths. This is the user's most repeated
instruction and overrides normal backwards-compatibility instincts.

- Never write compatibility branches, aliasing, or migration code for old data, old configs,
  old APIs, old schema, or old export formats. The user rebuilds the database instead.
- Deleted routes must return **404, not redirects**: `/history`, `/settings/navigation`,
  `/index.html`, `/script.js`, older `/api/*` paths.
- Unknown/legacy input is **rejected with a clear error**, never silently coerced.
- Current versions: schema **v13** (`storage/duckdb/schema.go`, pinned by `schema_test.go`),
  JSON/encrypted export **v10**, XLSX **v9**. There is no migration between any earlier version.
- Do not remove the existing **fail-closed** version checks. Prefer reporting "incompatible version"
  over quietly accepting data that could corrupt the user's database.

## Licensing Bootstrap

`internal/licensing/keys.go` ships with `builtinPublicKeyBase64 = ""`. Until build-time injection
replaces it, the app runs in `unconfigured` state and **recording and backup are disabled**. This is
expected, not a bug. Use `scripts/dev-run.ps1` for development: it writes a development license into
the real data directory. Production keys are generated on `cmd/license-keygen` (private key never
enters this repo).

## Working With the User

- Communicate in Chinese. UI copy, error messages, docs, and commit messages included.
- Grading and reporting: state concrete evidence (test counts, `-race` results, exit codes, specific
  viewports, console error counts), never "all green". Unverified items must be listed separately and
  must **not** be counted as passing.
- Classify findings as defect / conditional risk / hardening. Uncertain means say uncertain — do not
  present guesses as facts.
- Do not conflate "expanding review scope" with "passing this round". Scope creep here has caused
  multi-day non-converging loops and user frustration. Freeze scope, fix confirmed defects, then
  re-review as a separate pass.
- Verify the environment before declaring anything unsupported. In particular, never claim
  "no C compiler" without checking `D:\mingw64\bin\gcc.exe`.
- Main-package tests normally take 270–480s (~464s with `-race`). Absence of output is not success;
  wait for an explicit exit code.

## 首次公开发布约束

- 先读 `docs/OSS_RELEASE_REPORT.md`、`docs/GITHUB_PUBLISH_PLAN.md` 与 `docs/LICENSE_REVIEW.md`。许可证未批准前不得写入最终 LICENSE 或称为完整开源。
- 2026-10-21 维护者已授权选择 Apache-2.0，署名 ClipVault contributors；全部自有源码与文档开放、商业激活实现保留，联系方式使用 foobar 占位。许可证选择无需再次确认，第三方许可和公开发布仍需各自满足条件。
- 不读取真实剪贴板正文，不运行清库/重置脚本，不触碰正式运行实例及其数据库。完整开发启动必须显式设置独立 `CLIPVAULT_DATA_DIR`；dev-run 会写凭据，优先用隔离预览。
- 原生 Windows 验收用虚构文本/富文本/图片/文件，验证托盘、恢复、退出重启、备份恢复；Web 预览不替代真实跨应用验收。截图只用隔离数据，日志输出不得包含密码、Token、激活码或用户正文。
- `python -X utf8 scripts/audit-oss.py` 只读扫描所有引用历史、索引和工作区，仅记录位置，不因没有正则命中就宣称安全。`.gitignore` 不会移除已跟踪内容或旧历史。
- `go test -race ./... -count=1 -timeout=30m` 给大型备份用例足够时间。漏洞扫描用文本模式并核对发现，不以 JSON 模式退出码 0 代替风险判断。
- 未经用户明确“批准公开发布”，不 push、不建公开仓库/Release、不提交申请，不将整个工作目录打包。2026-10-21 用户另行授权新建发布分支清理内部资料历史：仅 codex/oss-release-preparation 可用于该清理，原 codex/clipvault-modular-refactor 固定在保存提交 2efd52d，不再改写或移动。公开 main 必须来自清理后的发布分支，保留源码演进；不创建无历史替代仓库，不上传原分支、其他分支或 stash。详见 docs/OSS_HISTORY_CLEANUP.md。
- 2026-10-21 维护者随后授权由其登录 GitHub、其余源码发布操作交由 Codex 执行；目标为 light8771/clipvault，仅公开清理后的源码 main。此授权不包含 EXE/Release 或 OpenAI 申请提交；原开发分支与本地 origin 仍保持不变。
