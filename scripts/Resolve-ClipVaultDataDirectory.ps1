function Assert-ClipVaultRegularDirectory {
    param([string]$Path, [string]$Description)

    $pathRoot = [System.IO.Path]::GetPathRoot($Path)
    $current = $Path
    while ($true) {
        $item = Get-Item -LiteralPath $current -Force -ErrorAction Stop
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "$Description must not use a symbolic link or junction: $current"
        }
        if ([string]::Equals($current.TrimEnd('\', '/'), $pathRoot.TrimEnd('\', '/'), [System.StringComparison]::OrdinalIgnoreCase)) {
            return
        }
        $current = Split-Path -Parent $current
    }
}

function Resolve-ClipVaultDataDirectory {
    param([string]$DataDir)

    if (-not [string]::IsNullOrWhiteSpace($DataDir)) {
        $DataDir = $DataDir.Trim()
        if ($DataDir -notmatch '^(?:[A-Za-z]:[\\/]|\\\\)') {
            throw 'DataDir must be an absolute path'
        }
        $resolvedDataDir = [System.IO.Path]::GetFullPath($DataDir)
        if (Test-Path -LiteralPath $resolvedDataDir -PathType Container) {
            Assert-ClipVaultRegularDirectory $resolvedDataDir 'DataDir'
        }
        return $resolvedDataDir
    }
    if (-not [string]::IsNullOrWhiteSpace($env:CLIPVAULT_DATA_DIR)) {
        if ($env:CLIPVAULT_DATA_DIR -notmatch '^(?:[A-Za-z]:[\\/]|\\\\)') {
            throw 'CLIPVAULT_DATA_DIR must be an absolute path'
        }
        return [System.IO.Path]::GetFullPath($env:CLIPVAULT_DATA_DIR)
    }

    $applicationRoot = Split-Path -Parent $PSScriptRoot
    if (Test-Path -LiteralPath (Join-Path $applicationRoot 'portable.flag') -PathType Leaf) {
        return (Join-Path $applicationRoot 'data')
    }

    $controlRoot = $env:LOCALAPPDATA
    if ([string]::IsNullOrWhiteSpace($controlRoot)) {
        $controlRoot = $env:APPDATA
    }
    if ([string]::IsNullOrWhiteSpace($controlRoot)) {
        $controlRoot = [Environment]::GetFolderPath('ApplicationData')
    }
    $defaultDataDir = Join-Path $controlRoot 'ClipVault'
    $locationPath = Join-Path $defaultDataDir 'storage-location.json'
    try {
        $locationFile = Get-Item -LiteralPath $locationPath -Force -ErrorAction Stop
    } catch [System.Management.Automation.ItemNotFoundException] {
        return $defaultDataDir
    } catch {
        throw "Unable to access ClipVault storage location: $($_.Exception.Message)"
    }
    if ($locationFile -isnot [System.IO.FileInfo]) {
        throw "ClipVault storage location is not a file: $locationPath"
    }

    try {
        $location = Get-Content -LiteralPath $locationPath -Raw | ConvertFrom-Json
    } catch {
        throw "Unable to read ClipVault storage location: $($_.Exception.Message)"
    }
    $configuredDataDir = [string]$location.dataDir
    if ([string]::IsNullOrWhiteSpace($configuredDataDir)) {
        throw "ClipVault storage location is missing dataDir: $locationPath"
    }
    $configuredDataDir = $configuredDataDir.Trim()
    if ($configuredDataDir -notmatch '^(?:[A-Za-z]:[\\/]|\\\\)') {
        throw "Configured ClipVault data directory must be an absolute path: $locationPath"
    }
    $configuredDataDir = [System.IO.Path]::GetFullPath($configuredDataDir)
    if (-not (Test-Path -LiteralPath $configuredDataDir -PathType Container)) {
        throw "Configured ClipVault data directory does not exist: $configuredDataDir"
    }
    Assert-ClipVaultRegularDirectory $configuredDataDir 'Configured ClipVault data directory'
    return $configuredDataDir
}
