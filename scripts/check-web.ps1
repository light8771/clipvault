param(
    [string]$Node = "node"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$web = Join-Path $root "internal\clipvault\web"
$parts = @(
    "bootstrap.js",
    "auth.js",
    "history.js",
    "files.js",
    "appearance.js",
    "settings.js",
    "config-form.js",
    "transfer.js",
    "groups.js",
    "lifecycle.js"
)

& $Node --check (Join-Path $web "api.js")
if ($LASTEXITCODE -ne 0) { throw "api.js syntax check failed" }
& $Node --check (Join-Path $web "store.js")
if ($LASTEXITCODE -ne 0) { throw "store.js syntax check failed" }

$temporaryModule = Join-Path ([IO.Path]::GetTempPath()) ("clipvault-app-{0}.mjs" -f [guid]::NewGuid().ToString("N"))
try {
    $content = foreach ($part in $parts) {
        Get-Content -Raw (Join-Path $web ("app\" + $part))
    }
    [IO.File]::WriteAllText($temporaryModule, ($content -join "`n"), [Text.UTF8Encoding]::new($false))
    & $Node --check $temporaryModule
    if ($LASTEXITCODE -ne 0) { throw "composed application module syntax check failed" }
} finally {
    Remove-Item -LiteralPath $temporaryModule -Force -ErrorAction SilentlyContinue
}

& $Node --test (Join-Path $web "api_store_test.mjs") (Join-Path $web "theme_utils_test.js")
if ($LASTEXITCODE -ne 0) { throw "web unit tests failed" }
