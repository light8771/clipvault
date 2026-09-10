#Requires -Version 5.0
<#
.SYNOPSIS
    一条龙：为单个订单/用户生成独立密钥对，烧录公钥到 exe，打包分发。

.DESCRIPTION
    每个订单/用户使用独立的 Ed25519 密钥对：
    - 私钥由维护者按订单保存，用于后续为该用户签发激活码
    - 公钥烧录进给该用户的 ClipVault.exe
    - 即使某个私钥泄露，只影响该用户，不影响其他订单

    流程：
    1. 生成 UUID 作为 KeyID（每个订单唯一）
    2. 在 signing\orders\<OrderID>\ 下生成 Ed25519 密钥对 + 口令
    3. 用公钥通过 Garble 构建 ClipVault.exe
    4. exe 输出到 signing\orders\<OrderID>\ClipVault.exe
    5. 生成 manifest.json 记录订单信息、KeyID、公钥、构建哈希

    所有产物都在 signing\orders\<OrderID>\ 下，被 .gitignore 排除不进 Git。

.PARAMETER OrderID
    订单号或用户标识，用于命名输出目录。未传时交互式询问。
    建议用订单号，便于后续追溯。

.PARAMETER UserNote
    用户备注（邮箱、昵称等），写入 manifest.json 便于追溯。可选。

.PARAMETER GarbleSeed
    Garble 混淆 seed（Base64）。未传时生成随机 seed。

.PARAMETER Output
    exe 输出路径。默认 signing\orders\<OrderID>\ClipVault.exe。

.EXAMPLE
    .\signing\build-for-order.ps1 -OrderID ORDER-2026-000001 -UserNote "user@example.com"

.EXAMPLE
    # 全交互式
    .\signing\build-for-order.ps1
#>
[CmdletBinding()]
param(
    [string]$OrderID,
    [string]$UserNote,
    [string]$GarbleSeed,
    [string]$Output
)

$ErrorActionPreference = "Stop"

# === 定位项目根 ===
if (-not $PSScriptRoot) { throw "无法定位脚本路径，请用 PowerShell 3.0+" }
$ScriptDir = $PSScriptRoot
$ProjectRoot = [IO.Path]::GetFullPath((Join-Path $ScriptDir ".."))
if (-not (Test-Path (Join-Path $ProjectRoot "cmd\license-keygen\main.go"))) {
    throw "项目根目录结构异常：$ProjectRoot 下找不到 cmd\license-keygen\main.go"
}

# === 交互式收集参数 ===
if ([string]::IsNullOrWhiteSpace($OrderID)) {
    Write-Host ""
    Write-Host "订单号或用户标识，用于命名输出目录和追溯。" -ForegroundColor Cyan
    $OrderID = Read-Host "请输入 OrderID（例如 ORDER-2026-000001）"
    if ([string]::IsNullOrWhiteSpace($OrderID)) { throw "OrderID 不能为空" }
}
# 清理 OrderID 用于目录名（只保留字母数字连字符下划线）
$SafeOrderID = $OrderID -replace '[^\w\-]', '_'

if ([string]::IsNullOrWhiteSpace($UserNote)) {
    Write-Host ""
    Write-Host "用户备注（邮箱/昵称等），写入 manifest.json 便于追溯。可留空。" -ForegroundColor Cyan
    $UserNote = Read-Host "请输入用户备注（可留空）"
}

# === 生成 UUID 作为 KeyID ===
$Uuid = [Guid]::NewGuid().ToString()
# KeyID 要求 ^[a-z0-9][a-z0-9-]{2,63}$，UUID 是 36 字符小写十六进制+连字符，符合
$KeyID = "cv-$Uuid"
Write-Host ""
Write-Host "本次订单的 KeyID：$KeyID" -ForegroundColor Green

# === 创建订单目录 ===
$OrderDir = Join-Path $ScriptDir "orders\$SafeOrderID"
if (Test-Path $OrderDir) {
    throw "订单目录已存在：$OrderDir。如需重新构建，请先手动备份并删除该目录。"
}
New-Item -ItemType Directory -Force -Path $OrderDir | Out-Null

# === 生成口令文件 ===
$PassphraseFile = Join-Path $OrderDir "key-passphrase.txt"
$PassBytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($PassBytes)
[IO.File]::WriteAllBytes($PassphraseFile, $PassBytes)
Write-Host "已生成口令文件：$PassphraseFile ($((Get-Item $PassphraseFile).Length) 字节)"

# === 生成密钥对 ===
$PrivateKeyPath = Join-Path $OrderDir "$KeyID.ed25519"
Write-Host ""
Write-Host "生成 Ed25519 密钥对..." -ForegroundColor Cyan

Push-Location $ProjectRoot
try {
    $env:CGO_ENABLED = "0"
    $genOutput = & go run ./cmd/license-keygen generate-key -out $PrivateKeyPath -passphrase-file $PassphraseFile 2>&1
    $genOutput | ForEach-Object { Write-Host $_ }
    if ($LASTEXITCODE -ne 0) { throw "generate-key 失败，退出码 $LASTEXITCODE" }
} finally {
    Pop-Location
}

if (-not (Test-Path $PrivateKeyPath)) { throw "私钥未生成：$PrivateKeyPath" }
if (-not (Test-Path "$PrivateKeyPath.pub")) { throw "公钥未生成：$PrivateKeyPath.pub" }

$PublicKey = (Get-Content "$PrivateKeyPath.pub" -Raw).Trim()
Write-Host "密钥对生成成功" -ForegroundColor Green
Write-Host "  私钥：$PrivateKeyPath"
Write-Host "  公钥：$PrivateKeyPath.pub"
Write-Host "  公钥内容：$PublicKey"

# === 构建 exe ===
if ([string]::IsNullOrWhiteSpace($GarbleSeed)) {
    # 默认用随机 seed（每次构建不同，更安全）
    # Garble 的 -seed 只用前 8 字节，所以生成 8 字节即可，避免 warning
    $SeedBytes = New-Object byte[] 8
    [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($SeedBytes)
    $GarbleSeed = [Convert]::ToBase64String($SeedBytes)
    Write-Host ""
    Write-Host "已生成随机 Garble seed（记录在 manifest.json 中便于复现）" -ForegroundColor Green
}

if ([string]::IsNullOrWhiteSpace($Output)) {
    $Output = Join-Path $OrderDir "ClipVault.exe"
}

Write-Host ""
Write-Host "构建 ClipVault.exe（Garble 混淆）..." -ForegroundColor Cyan
Write-Host "  KeyID      : $KeyID"
Write-Host "  GarbleSeed : $GarbleSeed"
Write-Host "  输出       : $Output"

$env:CGO_ENABLED = "1"
$env:PATH = "D:\mingw64\bin;" + $env:PATH

Push-Location $ProjectRoot
try {
    # build-release.ps1 内部设了 $ErrorActionPreference="Stop"，garble 的 warning
    # 会输出到 stderr，这里用 2>&1 合并流，避免触发外层 try/catch
    $buildOutput = & .\scripts\build-release.ps1 -Output $Output -Obfuscate `
        -KeyId $KeyID -PublicKey $PublicKey -GarbleSeed $GarbleSeed 2>&1
    $buildOutput | ForEach-Object { Write-Host $_ }
    if ($LASTEXITCODE -ne 0) { throw "build-release.ps1 失败，退出码 $LASTEXITCODE" }
} finally {
    Pop-Location
}

if (-not (Test-Path $Output)) { throw "exe 未生成：$Output" }
$ExeHash = (Get-FileHash -Algorithm SHA256 $Output).Hash
$ExeSize = (Get-Item $Output).Length

Write-Host ""
Write-Host "构建成功！" -ForegroundColor Green
Write-Host "  exe    ：$Output"
Write-Host "  大小   ：$ExeSize 字节"
Write-Host "  SHA256 ：$ExeHash"

# === 生成 manifest.json ===
$Manifest = [ordered]@{
    orderId       = $OrderID
    userNote      = $UserNote
    keyId         = $KeyID
    uuid          = $Uuid
    publicKey     = $PublicKey
    privateKey    = $PrivateKeyPath
    passphrase    = $PassphraseFile
    garbleSeed    = $GarbleSeed
    exePath       = $Output
    exeSha256     = $ExeHash
    exeSize       = $ExeSize
    buildTime     = (Get-Date).ToString("yyyy-MM-dd HH:mm:sszzz")
    builder       = $env:USERNAME
    machine       = $env:COMPUTERNAME
    projectRoot   = $ProjectRoot
}

$ManifestPath = Join-Path $OrderDir "manifest.json"
$Manifest | ConvertTo-Json -Depth 5 | Out-File -FilePath $ManifestPath -Encoding UTF8
Write-Host ""
Write-Host "已生成订单清单：$ManifestPath" -ForegroundColor Green

# === 总结 ===
Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "订单 $OrderID 一条龙完成" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "产物目录：$OrderDir"
Write-Host ""
Write-Host "文件清单："
Get-ChildItem $OrderDir | ForEach-Object {
    Write-Host ("  {0,-40} {1,12} 字节" -f $_.Name, $_.Length)
}
Write-Host ""
# 根据订单号生成建议的 LicenseID（issue-license.ps1 要求 ^CV-[A-Z0-9-]{2,62}$）
$SuggestedLicenseID = "CV-" + ($SafeOrderID -replace '[^A-Z0-9-]', '').ToUpper() + "-001"
if ($SuggestedLicenseID.Length -gt 63) { $SuggestedLicenseID = $SuggestedLicenseID.Substring(0, 63) }

Write-Host "下一步："
Write-Host "  1. 把 $Output 分发给用户"
Write-Host "  2. 用户安装后从「设置 → 授权」点击「复制购买信息」，把整段内容发给你"
Write-Host "     （其中「机器码」一行是 64 位十六进制，即 issue-license.ps1 的 -MachineID 参数）"
Write-Host "  3. 用 issue-license.ps1 为该用户签发激活码："
Write-Host "     .\signing\issue-license.ps1 -Key `"$PrivateKeyPath`" -PassphraseFile `"$PassphraseFile`" -KeyID `"$KeyID`" -LicenseID $SuggestedLicenseID -MachineID <用户机器码>"
Write-Host ""
Write-Host "  4. 私钥和口令文件已保存在订单目录，妥善备份"
Write-Host "  5. manifest.json 记录了本次构建的所有信息，便于追溯"
