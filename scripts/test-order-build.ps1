#Requires -Version 5.0
# Exercise the real order -> release scripts with fake external executables.
# Never generates production keys or distributes an executable.
$ErrorActionPreference = 'Stop'
$repository = Split-Path -Parent $PSScriptRoot
$tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$fixture = Join-Path $tempRoot ('clipvault-order-test-' + [Guid]::NewGuid().ToString('N'))
$publicKey = [Convert]::ToBase64String((New-Object byte[] 32)).TrimEnd('=')
$seed = 'AQIDBAUGBwg='
$testState = @{ Calls = [Collections.Generic.List[object]]::new(); Fail = $false }
$savedEnvironment = @{}
foreach ($name in @('PATH', 'CGO_ENABLED', 'CC', 'GOGARBLE', 'CLIPVAULT_LICENSE_KEY_ID', 'CLIPVAULT_LICENSE_PUBLIC_KEY', 'CLIPVAULT_GARBLE_SEED')) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
function go {
    if ($args[0] -ne 'run' -or $args[1] -ne './cmd/license-keygen' -or $args[2] -ne 'generate-key') {
        throw 'Unexpected go invocation; order builds must use garble'
    }
    $outIndex = [Array]::IndexOf($args, '-out')
    if ($outIndex -lt 0) { throw 'Missing key output path' }
    $keyPath = $args[$outIndex + 1]
    [IO.File]::WriteAllText($keyPath, 'test fixture only')
    [IO.File]::WriteAllText("$keyPath.pub", $publicKey)
    $global:LASTEXITCODE = 0
}
function garble {
    $testState.Calls.Add(@($args))
    if ($testState.Fail) { $global:LASTEXITCODE = 7; return }
    $outIndex = [Array]::IndexOf($args, '-o')
    if ($outIndex -lt 0) { throw 'Missing executable output path' }
    [IO.File]::WriteAllText($args[$outIndex + 1], 'test executable fixture only')
    $global:LASTEXITCODE = 0
}
try {
    New-Item -ItemType Directory -Path (Join-Path $fixture 'signing'), (Join-Path $fixture 'scripts'), (Join-Path $fixture 'cmd/license-keygen') -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $repository 'signing/build-for-order.ps1') -Destination (Join-Path $fixture 'signing')
    Copy-Item -LiteralPath (Join-Path $repository 'scripts/build-release.ps1') -Destination (Join-Path $fixture 'scripts')
    [IO.File]::WriteAllText((Join-Path $fixture 'cmd/license-keygen/main.go'), '// fixture')
    # Stale environment values must not override this order's explicit arguments.
    $env:CLIPVAULT_LICENSE_KEY_ID = 'stale-key'
    $env:CLIPVAULT_LICENSE_PUBLIC_KEY = 'stale-public-key'
    $env:CLIPVAULT_GARBLE_SEED = 'stale-seed'
    & (Join-Path $fixture 'signing/build-for-order.ps1') -OrderID acceptance -UserNote test -GarbleSeed $seed
    $manifestPath = Join-Path $fixture 'signing/orders/acceptance/manifest.json'
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ($testState.Calls.Count -ne 1) { throw 'Expected exactly one garble build' }
    $call = $testState.Calls[0]
    foreach ($arg in @("-seed=$seed", '-literals', 'build', '-trimpath')) {
        if ($call -notcontains $arg) { throw "Missing garble argument: $arg" }
    }
    $flags = $call | Where-Object { $_ -like '-ldflags=*' }
    if (-not $flags.Contains("builtinKeyID=$($manifest.keyId)") -or -not $flags.Contains("builtinPublicKeyBase64=$publicKey")) {
        throw 'Embedded license arguments do not match the order manifest'
    }
    if ($manifest.garbleSeed -ne $seed -or $manifest.exeSha256 -ne (Get-FileHash -LiteralPath $manifest.exePath).Hash) {
        throw 'Manifest does not describe the built artifact'
    }
    & (Join-Path $fixture 'signing/build-for-order.ps1') -OrderID random -UserNote test
    $randomManifest = Get-Content -LiteralPath (Join-Path $fixture 'signing/orders/random/manifest.json') -Raw | ConvertFrom-Json
    if ($testState.Calls.Count -ne 2 -or [Convert]::FromBase64String($randomManifest.garbleSeed).Length -ne 8 -or
        $testState.Calls[1] -notcontains "-seed=$($randomManifest.garbleSeed)") {
        throw 'Generated seed was not passed unchanged to garble and the manifest'
    }
    $testState.Fail = $true
    $failed = $false
    try { & (Join-Path $fixture 'signing/build-for-order.ps1') -OrderID failure -UserNote test -GarbleSeed $seed } catch { $failed = $true }
    if (-not $failed -or (Test-Path -LiteralPath (Join-Path $fixture 'signing/orders/failure/manifest.json'))) {
        throw 'Failed build was accepted as a completed order'
    }
    Write-Host 'Order build acceptance passed (garble arguments, manifest, failure propagation).'
} finally {
    foreach ($name in $savedEnvironment.Keys) { [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process') }
    $resolvedFixture = [IO.Path]::GetFullPath($fixture)
    $tempPrefix = $tempRoot.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    if (-not $resolvedFixture.StartsWith($tempPrefix, [StringComparison]::OrdinalIgnoreCase)) { throw 'Fixture escaped temporary directory' }
    if (Test-Path -LiteralPath $resolvedFixture) { Remove-Item -LiteralPath $resolvedFixture -Recurse -Force }
}
