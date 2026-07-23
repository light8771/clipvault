param(
    [string]$KeyId = $env:CLIPVAULT_LICENSE_KEY_ID,
    [string]$PrivateKey = "signing\cv-dev.ed25519",
    [string]$PassphraseFile = "signing\key-passphrase-dev.txt",
    [string]$PublicKeyFile = "signing\cv-dev.ed25519.pub"
)

$ErrorActionPreference = "Stop"

# Defaults align with the repo's dev key pair (see signing/.gitignore).
if ([string]::IsNullOrWhiteSpace($KeyId)) {
    $KeyId = "cv-main-2026"
}
if ($KeyId -notmatch '^[a-z0-9][a-z0-9-]{2,63}$') {
    throw "Invalid KeyID: $KeyId"
}

# Resolve repo root from this script's location so it works from any cwd.
$repoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $repoRoot

# Step 1: ensure a dev key pair exists. generate-key auto-creates the
# passphrase file if missing, so the user doesn't have to pre-stage anything.
if (-not (Test-Path $PrivateKey) -or -not (Test-Path $PublicKeyFile)) {
    Write-Host "No dev key pair found, generating one..."
    & go run ./cmd/license-keygen generate-key -out $PrivateKey -passphrase-file $PassphraseFile
    if ($LASTEXITCODE -ne 0) { throw "generate-key failed" }
}

# Step 2: read the public key (injected into clipvault via -ldflags -X).
$publicKey = (Get-Content $PublicKeyFile -Raw).Trim()
$padded = $publicKey
while (($padded.Length % 4) -ne 0) { $padded += "=" }
try {
    $publicBytes = [Convert]::FromBase64String($padded)
} catch {
    throw "Public key in $PublicKeyFile is not valid Base64"
}
if ($publicBytes.Length -ne 32) {
    throw "Public key in $PublicKeyFile must decode to 32 bytes (got $($publicBytes.Length))"
}

# Step 3: CGO + C compiler check (DuckDB requires it).
$env:CGO_ENABLED = "1"
$cc = Get-Command gcc -ErrorAction SilentlyContinue
if (-not $cc) {
    # Support common standalone MinGW-w64 locations without requiring a
    # permanent PATH change. D:\mingw64 is used by the local dev setup.
    $gccCandidates = @(
        "D:\mingw64\bin\gcc.exe",
        "C:\mingw64\bin\gcc.exe"
    )
    foreach ($gccCandidate in $gccCandidates) {
        if (-not (Test-Path -Path $gccCandidate -PathType Leaf)) {
            continue
        }
        $gccDir = Split-Path -Parent $gccCandidate
        $env:Path = $gccDir + [IO.Path]::PathSeparator + $env:Path
        $env:CC = $gccCandidate
        $cc = Get-Command gcc -ErrorAction SilentlyContinue
        break
    }
}
if (-not $cc) {
    throw "gcc not found; install MinGW-w64 or place it in D:\mingw64\bin (or add its bin directory to PATH)"
}

# Step 4: sign a perpetual license for the current machine and write it
# directly to the receipt file, so clipvault starts fully activated.
$localAppData = $env:LOCALAPPDATA
if ([string]::IsNullOrWhiteSpace($localAppData)) {
    $localAppData = [Environment]::GetFolderPath("LocalApplicationData")
}
$receiptPath = Join-Path $localAppData "ClipVault\license\clipvault.lic"

Write-Host "Activating current machine for dev..."
& go run ./cmd/dev-activate `
    -key $PrivateKey `
    -passphrase-file $PassphraseFile `
    -receipt-path $receiptPath `
    -kid $KeyId
if ($LASTEXITCODE -ne 0) { throw "dev-activate failed" }

# Step 5: launch clipvault with the dev public key baked in via ldflags.
$ldflags = "-X clipboard_ext/internal/licensing.builtinKeyID=$KeyId " +
    "-X clipboard_ext/internal/licensing.builtinPublicKeyBase64=$publicKey"

Write-Host ""
Write-Host "Starting clipvault in dev mode (console window, dev-activated)..."
Write-Host "  KeyID:       $KeyId"
Write-Host "  CGO:         $env:CGO_ENABLED"
Write-Host "  CC:          $($cc.Source)"
Write-Host "  Receipt:     $receiptPath"
Write-Host ""

# go run without -H=windowsgui keeps a console window, handy for logs and debug output.
& go run "-ldflags=$ldflags" .\cmd\clipvault @args
if ($LASTEXITCODE -ne 0) {
    throw "go run failed with exit code $LASTEXITCODE"
}
