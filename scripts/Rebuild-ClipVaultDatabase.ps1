[CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
param(
    [string]$DataDir,
    [switch]$SkipStop
)

$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'Resolve-ClipVaultDataDirectory.ps1')
$resolvedDataDir = Resolve-ClipVaultDataDirectory $DataDir
if (-not (Test-Path -LiteralPath $resolvedDataDir -PathType Container)) {
    throw "ClipVault data directory does not exist: $resolvedDataDir"
}

if (-not $SkipStop) {
    Get-Process -Name 'ClipVault' -ErrorAction SilentlyContinue | ForEach-Object {
        $processId = $_.Id
        if ($PSCmdlet.ShouldProcess("ClipVault process $processId", 'Stop')) {
            Stop-Process -Id $processId -Force
            Wait-Process -Id $processId -Timeout 10 -ErrorAction SilentlyContinue
            if (Get-Process -Id $processId -ErrorAction SilentlyContinue) {
                throw "ClipVault process $processId did not exit within 10 seconds"
            }
        }
    }
}

$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backupDir = Join-Path $resolvedDataDir "rebuild-backup-$timestamp"

$names = @(
    'clipvault.duckdb',
    'clipvault.duckdb.wal',
    'clipvault.key',
    'clipvault.recovery',
    'clipvault.state',
    'recovery-appearance.json'
)
$patterns = @('brand-logo-*', 'brand-favicon-*', 'background-media-*')
$files = [System.Collections.Generic.List[System.IO.FileInfo]]::new()

foreach ($name in $names) {
    $path = Join-Path $resolvedDataDir $name
    if (Test-Path -LiteralPath $path -PathType Leaf) {
        $files.Add((Get-Item -LiteralPath $path))
    }
}
foreach ($pattern in $patterns) {
    Get-ChildItem -LiteralPath $resolvedDataDir -File -Filter $pattern | ForEach-Object {
        $files.Add($_)
    }
}

if ($files.Count -eq 0) {
    throw "No ClipVault database or asset files were found in: $resolvedDataDir"
}

if ($PSCmdlet.ShouldProcess($backupDir, 'Create rebuild backup directory')) {
    New-Item -ItemType Directory -Path $backupDir | Out-Null
}

foreach ($file in $files) {
    if ($PSCmdlet.ShouldProcess($file.FullName, "Move to $backupDir")) {
        Move-Item -LiteralPath $file.FullName -Destination $backupDir
    }
}

if ($WhatIfPreference) {
    Write-Host 'WhatIf completed. No files were moved.'
    return
}

Write-Host "Previous data was moved to: $backupDir"
Write-Host 'Start ClipVault again. It will create a new encrypted database with default settings.'
