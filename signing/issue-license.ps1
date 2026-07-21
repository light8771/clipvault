#Requires -Version 5.0
<#
.SYNOPSIS
    用 ClipVault 离线私钥为指定机器码签发永久激活码。

.DESCRIPTION
    本脚本调用项目自带的 cmd/license-keygen issue 子命令，使用加密的 Ed25519 私钥
    对 { KeyID, LicenseID, MachineID, Edition=perpetual } 进行签名，生成 CV1.xxx.yyy
    格式的激活码。激活码绑定机器码，一次激活后当前设备永久使用。

    所有参数都可选：
    - 命令行传入：直接使用，适合自动化场景
    - 不传或传空：脚本交互式询问，带默认值，适合人工操作

    签发流程：
    1. 用户从客户端「设置 → 授权」复制完整机器码（64 个小写十六进制字符）；
    2. 维护者在本脚本指定 -MachineID 为该完整机器码；
    3. 脚本输出一行激活码，回传给用户；
    4. 用户在客户端粘贴激活码并点击「立即激活」。

.PARAMETER Key
    加密私钥路径（generate-key.ps1 生成的 .ed25519 或 .private-key 文件）。

.PARAMETER PassphraseFile
    口令文件路径（与 generate-key.ps1 使用的同一份）。

.PARAMETER KeyID
    Key ID，必须与客户端内置的 KeyID 一致（即 generate-key.ps1 生成时使用的 KeyID）。
    默认 cv-main-2026。

.PARAMETER LicenseID
    许可证编号，格式 CV-XXX，大写字母/数字/连字符，4-63 字符。
    建议维护一份「授权编号登记表」，记录签发时间、机器码、用户、编号。
    未传时脚本会基于当前日期+序号自动建议一个。

.PARAMETER MachineID
    完整机器码，64 个小写十六进制字符。从客户端「复制购买信息」获取，
    或调用 GET http://127.0.0.1:18765/api/license/machine 取 machineId 字段。

.EXAMPLE
    .\signing\issue-license.ps1 `
        -Key D:\private-keys\clipvault\cv-main-2026.ed25519 `
        -PassphraseFile D:\private-keys\clipvault\key-passphrase.txt `
        -KeyID cv-main-2026 `
        -LicenseID CV-2026-000001 `
        -MachineID 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

.EXAMPLE
    # 全交互式：什么都不传
    .\signing\issue-license.ps1

.NOTES
    安全要求：
    - 签发机应离线或专用，不要在构建机上签发；
    - 私钥和口令文件不要复制到项目目录；
    - 激活码可通过聊天/邮件回传给用户，但私钥和口令永远不能传输；
    - 建议把每次签发的 LicenseID、MachineID、签发时间、用户记录到离线登记表，
      便于后续追溯和换机重新签发。
#>
[CmdletBinding()]
param(
    [string]$Key,
    [string]$PassphraseFile,
    [string]$LicenseID,
    [string]$MachineID,
    [string]$KeyID
)

$ErrorActionPreference = "Stop"

# 用脚本自身位置定位项目根（signing/ 的父目录），这样无论从哪个 cwd 调用都能跑
# $PSScriptRoot 在 PowerShell 3.0+ 自动可用，最可靠
if (-not $PSScriptRoot) {
    throw "无法定位脚本路径，请用 PowerShell 3.0 或更高版本执行。"
}
$ScriptDir = $PSScriptRoot
$ProjectRoot = [IO.Path]::GetFullPath((Join-Path $ScriptDir ".."))
if (-not (Test-Path (Join-Path $ProjectRoot "cmd\license-keygen\main.go"))) {
    throw "项目根目录结构异常：$ProjectRoot 下找不到 cmd\license-keygen\main.go。请确认 signing\ 目录位于项目根下。"
}

# === 交互式收集参数 ===

# Key：默认查找脚本所在目录下的 .ed25519 文件
if ([string]::IsNullOrWhiteSpace($Key)) {
    $ExistingKeys = Get-ChildItem -Path $ScriptDir -Filter "*.ed25519" -File -ErrorAction SilentlyContinue
    if ($ExistingKeys.Count -eq 1) {
        $DefaultKey = $ExistingKeys[0].FullName
        Write-Host ""
        Write-Host "私钥路径（generate-key.ps1 生成的 .ed25519 文件）" -ForegroundColor Cyan
        $Key = Read-Host "请输入私钥路径（回车使用检测到的 $DefaultKey）"
        if ([string]::IsNullOrWhiteSpace($Key)) { $Key = $DefaultKey }
    } elseif ($ExistingKeys.Count -gt 1) {
        Write-Host ""
        Write-Host "私钥路径（在 signing\ 目录下检测到多个 .ed25519 文件）" -ForegroundColor Cyan
        $i = 0
        foreach ($k in $ExistingKeys) {
            Write-Host "  [$i] $($k.FullName)"
            $i++
        }
        $Idx = Read-Host "请输入序号选择，或直接输入完整路径"
        if ($Idx -match '^\d+$' -and [int]$Idx -lt $ExistingKeys.Count) {
            $Key = $ExistingKeys[[int]$Idx].FullName
        } else {
            $Key = $Idx
        }
    } else {
        Write-Host ""
        Write-Host "私钥路径（generate-key.ps1 生成的 .ed25519 文件）" -ForegroundColor Cyan
        $Key = Read-Host "请输入私钥路径"
        if ([string]::IsNullOrWhiteSpace($Key)) { throw "私钥路径不能为空" }
    }
}
if (-not (Test-Path $Key)) {
    throw "私钥文件不存在：$Key"
}

# PassphraseFile：默认查找脚本所在目录下的 key-passphrase.txt
if ([string]::IsNullOrWhiteSpace($PassphraseFile)) {
    $DefaultPass = Join-Path $ScriptDir "key-passphrase.txt"
    if (Test-Path $DefaultPass) {
        Write-Host ""
        Write-Host "口令文件路径（与 generate-key.ps1 使用的同一份）" -ForegroundColor Cyan
        $PassphraseFile = Read-Host "请输入口令文件路径（回车使用检测到的 $DefaultPass）"
        if ([string]::IsNullOrWhiteSpace($PassphraseFile)) { $PassphraseFile = $DefaultPass }
    } else {
        Write-Host ""
        Write-Host "口令文件路径（与 generate-key.ps1 使用的同一份）" -ForegroundColor Cyan
        $PassphraseFile = Read-Host "请输入口令文件路径"
        if ([string]::IsNullOrWhiteSpace($PassphraseFile)) { throw "口令文件路径不能为空" }
    }
}
if (-not (Test-Path $PassphraseFile)) {
    throw "口令文件不存在：$PassphraseFile"
}

# KeyID
if ([string]::IsNullOrWhiteSpace($KeyID)) {
    Write-Host ""
    Write-Host "KeyID 必须与客户端内置的 KeyID 一致。" -ForegroundColor Cyan
    $KeyID = Read-Host "请输入 KeyID（回车使用默认 cv-main-2026）"
    if ([string]::IsNullOrWhiteSpace($KeyID)) { $KeyID = "cv-main-2026" }
}
if ($KeyID -notmatch '^[a-z0-9][a-z0-9-]{2,63}$') {
    throw "KeyID 必须为小写字母/数字/连字符，长度 3-64，且以字母或数字开头：$KeyID"
}

# LicenseID
if ([string]::IsNullOrWhiteSpace($LicenseID)) {
    $Today = Get-Date
    $Suggested = "CV-$($Today.ToString('yyyy'))-$($Today.ToString('MMdd'))-001"
    Write-Host ""
    Write-Host "许可证编号，格式 CV-XXX，大写字母/数字/连字符。" -ForegroundColor Cyan
    Write-Host "建议维护一份离线授权编号登记表，避免重复。"
    $LicenseID = Read-Host "请输入 LicenseID（回车使用建议 $Suggested）"
    if ([string]::IsNullOrWhiteSpace($LicenseID)) { $LicenseID = $Suggested }
}
if ($LicenseID -notmatch '^CV-[A-Z0-9][A-Z0-9-]{2,62}$') {
    throw "LicenseID 必须形如 CV-XXX，大写字母/数字/连字符，长度 4-63：$LicenseID"
}

# MachineID
if ([string]::IsNullOrWhiteSpace($MachineID)) {
    Write-Host ""
    Write-Host "完整机器码，64 个小写十六进制字符。" -ForegroundColor Cyan
    Write-Host "从客户端「设置 → 授权 → 复制购买信息」获取，"
    Write-Host "或调用 GET http://127.0.0.1:18765/api/license/machine 取 machineId 字段。"
    $MachineID = Read-Host "请输入完整机器码"
    if ([string]::IsNullOrWhiteSpace($MachineID)) { throw "机器码不能为空" }
}
if ($MachineID -notmatch '^[0-9a-f]{64}$') {
    throw "MachineID 必须是 64 个小写十六进制字符：$MachineID"
}

# keygen 是纯 Go，不需要 CGO
$env:CGO_ENABLED = "0"

# 把相对路径转成绝对路径，避免 Push-Location 后失效
$Key = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Key)
$PassphraseFile = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($PassphraseFile)

# 切到项目根执行 go run（go run 需要在模块根目录）
Push-Location $ProjectRoot
try {

Write-Host ""
Write-Host "签发永久激活码..." -ForegroundColor Cyan
Write-Host "  KeyID      : $KeyID"
Write-Host "  LicenseID  : $LicenseID"
Write-Host "  MachineID  : $MachineID"
Write-Host ""

$Token = (& go run ./cmd/license-keygen issue `
    -key $Key `
    -passphrase-file $PassphraseFile `
    -kid $KeyID `
    -license-id $LicenseID `
    -machine $MachineID).Trim()

if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($Token)) {
    throw "keygen issue 失败，退出码 $LASTEXITCODE"
}

if ($Token -notmatch '^CV1\.') {
    throw "keygen 输出格式异常，不是有效的 CV1.xxx.yyy 激活码：$Token"
}

Write-Host "激活码（一行，回传给用户）：" -ForegroundColor Green
Write-Host ""
Write-Host $Token
Write-Host ""
Write-Host "请记录到离线授权编号登记表："
Write-Host "  LicenseID  = $LicenseID"
Write-Host "  MachineID  = $MachineID"
Write-Host "  KeyID      = $KeyID"
Write-Host "  签发时间    = $((Get-Date).ToString('yyyy-MM-dd HH:mm:sszzz'))"
Write-Host "  签发人      = $env:USERNAME"
Write-Host "  签发机      = $env:COMPUTERNAME"

} finally {
    Pop-Location
}
