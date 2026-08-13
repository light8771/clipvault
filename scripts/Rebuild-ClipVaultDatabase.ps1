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

$clipVaultProcesses = @(Get-Process -ErrorAction SilentlyContinue | Where-Object {
    $_.ProcessName -match '^(?i:clipvault.*|isolated-preview)$'
})

if ($SkipStop -and $clipVaultProcesses.Count -ne 0) {
    throw "ClipVault is running (PID: $($clipVaultProcesses.Id -join ', ')); -SkipStop cannot be used on an active data directory"
}

if (-not $SkipStop) {
    $clipVaultProcesses | ForEach-Object {
        $processId = $_.Id
        if ($PSCmdlet.ShouldProcess("ClipVault process $processId", 'Stop')) {
            $null = $_.CloseMainWindow()
            Wait-Process -Id $processId -Timeout 5 -ErrorAction SilentlyContinue
            if (Get-Process -Id $processId -ErrorAction SilentlyContinue) {
                Stop-Process -Id $processId -Force
                Wait-Process -Id $processId -Timeout 10 -ErrorAction SilentlyContinue
            }
            if (Get-Process -Id $processId -ErrorAction SilentlyContinue) {
                throw "ClipVault process $processId did not exit"
            }
        }
    }
}

$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backupDir = Join-Path $resolvedDataDir "rebuild-backup-$timestamp"

# database_layout.go 把数据库文件放到 dataDir/database/ 子目录下。
# 根目录仅保留 recovery-appearance.json 和品牌/背景资源。
$databaseDir = Join-Path $resolvedDataDir 'database'
$databaseNames = @(
    'clipvault.duckdb',
    'clipvault.duckdb.wal',
    'clipvault.key',
    'clipvault.recovery',
    'clipvault.state'
)
$rootNames = @('recovery-appearance.json')
$rootPatterns = @('brand-logo-*', 'brand-favicon-*', 'background-media-*')
$files = [System.Collections.Generic.List[System.IO.FileInfo]]::new()

foreach ($name in $databaseNames) {
    $path = Join-Path $databaseDir $name
    if (Test-Path -LiteralPath $path -PathType Leaf) {
        $files.Add((Get-Item -LiteralPath $path))
    }
}
foreach ($name in $rootNames) {
    $path = Join-Path $resolvedDataDir $name
    if (Test-Path -LiteralPath $path -PathType Leaf) {
        $files.Add((Get-Item -LiteralPath $path))
    }
}
foreach ($pattern in $rootPatterns) {
    Get-ChildItem -LiteralPath $resolvedDataDir -File -Filter $pattern | ForEach-Object {
        $files.Add($_)
    }
}

if ($files.Count -eq 0) {
    Write-Host "ClipVault data directory is already empty: $resolvedDataDir"
    Write-Host 'No rebuild backup was created.'
    Write-Host 'Start ClipVault. It will create the current encrypted database schema and default settings.'
    return
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
