param(
    [Parameter(Mandatory = $true)]
    [string]$ExpectedDataDir,
    [switch]$PermanentlyDelete
)

$ErrorActionPreference = "Stop"

if (-not $PermanentlyDelete) {
    throw "Refusing to run without -PermanentlyDelete"
}

. (Join-Path $PSScriptRoot "Resolve-ClipVaultDataDirectory.ps1")
$expected = [IO.Path]::GetFullPath($ExpectedDataDir).TrimEnd("\", "/")
$resolved = [IO.Path]::GetFullPath((Resolve-ClipVaultDataDirectory)).TrimEnd("\", "/")
$workspace = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot)).TrimEnd("\", "/")
$root = [IO.Path]::GetPathRoot($resolved).TrimEnd("\", "/")

if (-not [string]::Equals($resolved, $expected, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Resolved data directory '$resolved' does not match expected '$expected'"
}
if ([string]::Equals($resolved, $root, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to remove a drive root"
}
if ([string]::Equals($resolved, $workspace, [StringComparison]::OrdinalIgnoreCase) -or
    $workspace.StartsWith($resolved + "\", [StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to remove the workspace or one of its parents"
}

Assert-ClipVaultRegularDirectory $resolved "Deletion target"
$running = @(Get-Process -ErrorAction SilentlyContinue | Where-Object {
    $_.ProcessName -match "^(?i:clipvault.*|isolated-preview)$"
})
if ($running.Count -ne 0) {
    throw "ClipVault processes are still running: $($running.Id -join ', ')"
}

Remove-Item -LiteralPath $resolved -Recurse -Force
New-Item -ItemType Directory -Path $resolved | Out-Null

$created = Get-Item -LiteralPath $resolved -Force
if (($created.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    throw "Recreated data directory unexpectedly became a reparse point"
}
if (@(Get-ChildItem -LiteralPath $resolved -Force).Count -ne 0) {
    throw "Recreated data directory is not empty"
}

[PSCustomObject]@{
    DataDir = $resolved
    Empty = $true
    ReparsePoint = $false
}
