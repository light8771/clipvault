# ClipVault

ClipVault is a Windows 10/11 x64 clipboard history manager written in Go. It runs in the system tray and provides a local Web UI, search, favorites/groups, encrypted DuckDB storage, password-protected exports, scheduled directory/SMTP backups, and optional read-only HTTP Data API and MCP services.

**Source repository:** [light8771/clipvault](https://github.com/light8771/clipvault), branch `main`. Project-owned source and documentation use [Apache-2.0](LICENSE), attributed to `ClipVault contributors` in [NOTICE](NOTICE). Offline activation and signing tools remain included. License recipients may modify, build and redistribute it commercially; official paid binaries and support do not restrict these rights. Development history excludes internal materials; see [the cleanup record](docs/OSS_HISTORY_CLEANUP.md). This is a source publication; binary distribution notices and Windows stable-release acceptance remain open. Security contacts are the placeholder `foobar`.

## Build and test

Requires Go **1.26.10**, CGO, and Node.js/npm for development checks. The verified Windows compiler is [Winlibs GCC 16.1.0 POSIX / MinGW-w64 14.0.0 UCRT r3](https://github.com/brechtsanders/winlibs_mingw/releases/tag/16.1.0posix-14.0.0-ucrt-r3); CI pins that archive and checks its SHA-256. MSYS2 GCC 16.2.0 produced unresolved symbols when linking this DuckDB static library on the GitHub runner. CSS/HTML and native JavaScript modules are embedded directly; there is no frontend build step.

Encrypted writes initialize DuckDB's officially signed `httpfs` extension for its OpenSSL provider. A cold start downloads the matching extension from DuckDB's core repository into the current user's extension cache; later starts use that cache. Offline installations need the matching signed extension prepared in advance. Initialization fails if installation or loading fails; the unsafe mbedTLS write option is never enabled. See [privacy details](PRIVACY.md).

```powershell
# Adjust this example compiler location to your own installation.
$env:PATH='D:\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED='1'
$env:CC='D:\mingw64\bin\gcc.exe'
npm ci
go test ./... -count=1
go test -race ./... -count=1 -timeout=30m
./scripts/check-web.ps1
./scripts/test-order-build.ps1
go run ./cmd/isolated-preview -lifetime 10m
```

The preview uses temporary synthetic/empty data and does not validate native clipboard recording. A normal build without an injected license public key is unconfigured: recording and backup are disabled. For full development, use `scripts/dev-run.ps1` only after explicitly setting `CLIPVAULT_DATA_DIR` to a separate test directory; it writes a development activation receipt. Never use production signing keys for testing.

Release builds use `scripts/build-release.ps1` with an approved key ID/public key. Garble is optional through `-Obfuscate`; no private key belongs on a build machine. The local review candidate is unsigned and uses a development public key; it is not a production release.

## Usage and privacy

Start `ClipVault.exe`, complete initial password setup, then use the tray menu to open history/settings or pause recording. The default history UI is `http://127.0.0.1:15678`. `Ctrl+Shift+Space` opens native search. A portable installation uses an empty `portable.flag` beside the executable, with `data/` and `logs/` directories beside it; no installer is included.

Default data root: `%LOCALAPPDATA%\ClipVault`, falling back to `%APPDATA%\ClipVault`. The database is encrypted, but managed images, restoration caches and logs may be plaintext. Rich-text capture can fetch referenced images; SMTP backups send data to the configured server/recipient. HTTP Data API and MCP are disabled by default and require channel-specific tokens when enabled. HTTP transport is not encrypted. See [privacy](PRIVACY.md) and [security](SECURITY.md).

OCR and semantic tags remain planned. Legacy database/config/export versions are rejected; no migrations are provided. Verify an independent backup before changing versions. Removing the executable does not remove stored personal data.

See the [Chinese README](README.md), [contribution guide](CONTRIBUTING.md), [third-party notices](THIRD_PARTY_NOTICES.md), [license review](docs/LICENSE_REVIEW.md), and [release readiness report](docs/OSS_RELEASE_REPORT.md). Public stars, downloads and adoption have not been established.
