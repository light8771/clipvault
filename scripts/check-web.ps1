param(
    [string]$Node = "node"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$web = Join-Path $root "internal\clipvault\web"
& $Node --check (Join-Path $web "api.js")
if ($LASTEXITCODE -ne 0) { throw "api.js syntax check failed" }
& $Node --check (Join-Path $web "store.js")
if ($LASTEXITCODE -ne 0) { throw "store.js syntax check failed" }
& $Node --check (Join-Path $web "startup.js")
if ($LASTEXITCODE -ne 0) { throw "startup.js syntax check failed" }
& $Node --check (Join-Path $web "recovery.js")
if ($LASTEXITCODE -ne 0) { throw "recovery.js syntax check failed" }

Get-ChildItem -LiteralPath (Join-Path $web "app") -File -Filter "*.js" | Sort-Object Name | ForEach-Object {
    & $Node --check $_.FullName
    if ($LASTEXITCODE -ne 0) { throw "$($_.Name) syntax check failed" }
}

$tests = @(
    Get-ChildItem -LiteralPath $web -File |
        Where-Object { $_.Name -match '(_test\.mjs|_test\.js)$' } |
        Sort-Object Name |
        ForEach-Object FullName
)
& $Node --test $tests
if ($LASTEXITCODE -ne 0) { throw "web unit tests failed" }
