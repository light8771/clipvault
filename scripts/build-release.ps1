param(
    [string]$Output = "dist\ClipVault.exe",
    [string]$KeyId = $env:CLIPVAULT_LICENSE_KEY_ID,
    [string]$PublicKey = $env:CLIPVAULT_LICENSE_PUBLIC_KEY,
    [string]$GarbleSeed = $env:CLIPVAULT_GARBLE_SEED,
    [switch]$Obfuscate
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($KeyId)) {
    throw "CLIPVAULT_LICENSE_KEY_ID is required"
}
if ($KeyId -notmatch '^[a-z0-9][a-z0-9-]{2,63}$') {
    throw "Invalid CLIPVAULT_LICENSE_KEY_ID"
}
if ([string]::IsNullOrWhiteSpace($PublicKey)) {
    throw "CLIPVAULT_LICENSE_PUBLIC_KEY is required"
}

$padded = $PublicKey
while (($padded.Length % 4) -ne 0) { $padded += "=" }
try {
    $publicBytes = [Convert]::FromBase64String($padded)
} catch {
    throw "CLIPVAULT_LICENSE_PUBLIC_KEY must be unpadded standard Base64"
}
if ($publicBytes.Length -ne 32) {
    throw "CLIPVAULT_LICENSE_PUBLIC_KEY must decode to 32 bytes"
}

if ($Obfuscate) {
    $garble = Get-Command garble -ErrorAction SilentlyContinue
    if (-not $garble) {
        throw "garble is not installed or not available in PATH"
    }
    if ([string]::IsNullOrWhiteSpace($GarbleSeed)) {
        throw "CLIPVAULT_GARBLE_SEED is required when -Obfuscate is selected"
    }
}

$env:CGO_ENABLED = "1"
$env:CC = "gcc"

$outputDir = Split-Path -Parent $Output
if ($outputDir) { New-Item -ItemType Directory -Force -Path $outputDir | Out-Null }

$ldflags = "-s -w -H=windowsgui " +
    "-X clipboard_ext/internal/licensing.builtinKeyID=$KeyId " +
    "-X clipboard_ext/internal/licensing.builtinPublicKeyBase64=$PublicKey"

if ($Obfuscate) {
    $env:GOGARBLE = "clipboard_ext"
    $prevEAP = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        & garble "-seed=$GarbleSeed" -literals build `
            -trimpath `
            -buildvcs=false `
            "-ldflags=$ldflags" `
            -o $Output `
            .\cmd\clipvault 2>&1 | ForEach-Object { Write-Host $_ }
    } finally {
        $ErrorActionPreference = $prevEAP
    }
} else {
    & go build `
        -trimpath `
        -buildvcs=false `
        "-ldflags=$ldflags" `
        -o $Output `
        .\cmd\clipvault
}

if ($LASTEXITCODE -ne 0) {
    throw "build failed"
}

$hash = Get-FileHash -Algorithm SHA256 -LiteralPath $Output
Write-Host "Release build complete: $($hash.Path)"
Write-Host "SHA256: $($hash.Hash)"
Write-Host "Sign this exact file with Authenticode before publishing."
