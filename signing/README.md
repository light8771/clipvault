# ClipVault 授权签发目录

本目录包含 ClipVault 永久授权（Ed25519 离线签发）的**签发脚本和流程文档**。

> **核心模型：一用户一密钥对**。每个订单/用户使用独立的 Ed25519 密钥对：
> - 私钥由维护者按订单保存，用于为该用户签发激活码
> - 公钥烧录进给该用户的 `ClipVault.exe`
> - 即使某个私钥泄露，只影响该用户，不影响其他订单

> **关键约束**：本目录**只放脚本和文档**，**不放私钥和口令**。
> 私钥/口令/订单目录通过 [.gitignore](./.gitignore) 排除；分发只分发编译后的 exe，不分发源码和私钥。

## 目录内容

| 文件 | 用途 | 是否进 Git |
|------|------|-----------|
| `README.md` | 本文档，签发流程 | 是 |
| `build-for-order.ps1` | 一条龙：生成密钥对 + 烧录公钥到 exe + 打包 | 是 |
| `issue-license.ps1` | 用私钥为指定机器码签发永久激活码 | 是 |
| `.gitignore` | 兜底排除私钥/口令/订单目录 | 是 |
| `orders/` | 按订单生成的密钥对和构建产物 | **否**（整目录被 gitignore） |
| `*.ed25519` / `*.private-key` | 加密私钥 | **否** |
| `*passphrase*.txt` | 私钥口令文件 | **否** |

## 流程总览

```
[维护者] build-for-order.ps1  →  生成密钥对 + 烧录公钥 + 构建 exe
    ↓
[用户]   安装 exe → 复制机器码给维护者
    ↓
[维护者] issue-license.ps1  →  用该订单私钥签发激活码
    ↓
[用户]   粘贴激活码 → 永久激活
```

## 一条龙：为订单构建定制 exe

`build-for-order.ps1` 完成全部构建步骤：生成 UUID 作为 KeyID → 生成密钥对 → 烧录公钥到 exe → 输出订单清单。

### 用法

```powershell
# 交互式（推荐人工操作）
.\signing\build-for-order.ps1

# 命令行参数（适合自动化）
.\signing\build-for-order.ps1 -OrderID "ORDER-2026-000001" -UserNote "user@example.com"
```

### 前置条件

- Go 工具链（`go run ./cmd/license-keygen` 需要）
- MinGW（`D:\mingw64\bin` 在 PATH 中，CGO 构建）
- Garble（`go install mvdan.cc/garble@latest` 一次即可）

### 产物

所有产物在 `signing\orders\<OrderID>\` 下：

```
signing\orders\ORDER-2026-000001\
├── ClipVault.exe                    ← 分发给用户
├── cv-<uuid>.ed25519                ← 私钥（妥善备份）
├── cv-<uuid>.ed25519.pub            ← 公钥
├── key-passphrase.txt               ← 口令（妥善备份）
└── manifest.json                    ← 订单清单（KeyID、公钥、SHA256、Garble seed 等）
```

`manifest.json` 记录了订单号、用户备注、KeyID、UUID、公钥、私钥路径、口令路径、Garble seed、exe 路径、SHA256、构建时间、构建者、机器名——便于后续追溯和复现。

### 安全说明

- **KeyID 是 UUID**：每个订单唯一，格式 `cv-<uuid>`，符合 `^[a-z0-9][a-z0-9-]{2,63}$`
- **Garble seed 随机**：每次构建用随机 32 字节 Base64 seed，记录在 manifest.json
- **私钥+口令不进 Git**：`.gitignore` 拦截 `*.ed25519`、`*passphrase*.txt`、`orders/`
- **公钥可公开**：公钥不是秘密，客户端用公钥验签

## 用户激活流程

### 用户侧：获取机器码

1. 用户运行 `ClipVault.exe`，双击托盘图标打开 Web 设置页；
2. 进入「设置 -> 授权」分区；
3. 点击「复制购买信息」，把包含产品、版本、完整机器码的信息回传给维护者。

完整机器码是 64 个小写十六进制字符。页面上的短分组机器码只用于人工识别，**签发必须使用完整机器码**。

### 维护者侧：签发激活码

在维护者机器执行（两种方式任选）。脚本会自动定位项目根，可以在任意目录下运行：

**方式 A：全交互式**（推荐人工操作，参数都不传）

```powershell
.\signing\issue-license.ps1
```

脚本会自动检测 `signing\` 目录下的 `.ed25519` 文件和 `key-passphrase.txt`，依次询问私钥路径、口令文件、KeyID、LicenseID、MachineID，每个都有默认值或提示。

**方式 B：命令行参数**（适合自动化脚本）

```powershell
.\signing\issue-license.ps1 `
    -Key "signing\orders\ORDER-2026-000001\cv-<uuid>.ed25519" `
    -PassphraseFile "signing\orders\ORDER-2026-000001\key-passphrase.txt" `
    -KeyID "cv-<uuid>" `
    -LicenseID CV-ORDER-2026-000001-001 `
    -MachineID 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

> KeyID、私钥路径、口令路径都可以从该订单的 `manifest.json` 里查到。

脚本会：
- 校验 KeyID / LicenseID / MachineID 格式；
- 调用 `go run ./cmd/license-keygen issue` 签发；
- 输出一行 `CV1.xxx.yyy` 格式的激活码；
- 提示记录到离线授权编号登记表。

### 维护者侧：登记

把每次签发的以下信息记录到离线登记表（不要进 Git）：

| 字段 | 示例 |
|------|------|
| OrderID | ORDER-2026-000001 |
| LicenseID | CV-ORDER-2026-000001-001 |
| MachineID | 0123456789abcdef... |
| KeyID | cv-c7c33625-423d-4fa8-8561-924d018b9584 |
| 签发时间 | 2026-07-21 14:30:00+08:00 |
| 签发人 | your-name |
| 签发机 | OFFLINE-SIGNER-01 |
| 用户标识 | 用户提供的订单号/邮箱/昵称 |
| 备注 | 首次激活 / 换机重发 / 丢失补发 |

### 用户侧：激活

1. 用户在客户端「设置 -> 授权」分区粘贴激活码；
2. 点击「立即激活」；
3. 客户端用内置公钥验签 + 校验机器码，通过后写入 DPAPI 保护的 `clipvault.lic`；
4. 录制功能启用，**当前设备永久使用**，不需要重启。

## 备份与归档

每个订单的 `signing\orders\<OrderID>\` 目录包含该用户的全套凭据：

- **必须备份**：`*.ed25519`（私钥）、`key-passphrase.txt`（口令）
- **建议归档**：`manifest.json`、`ClipVault.exe`（便于复现和追溯）

备份要求：
- 私钥文件和口令文件**分别**备份到至少两份不同离线介质（例如两个不同品牌的 USB + 加密压缩包）
- 备份介质离线保存，不要联网，不要放云盘
- manifest.json 可以随项目文档归档（不含私钥）

## 常见问题

### Q: 私钥丢了怎么办？

**无法补救**。该订单的用户无法换机重发激活码。只能为该用户重新跑 `build-for-order.ps1` 生成新密钥对和新 exe，用户重新安装并激活。所以**每个订单的私钥必须妥善备份**。

### Q: 用户换电脑/重装系统怎么办？

机器码会变，旧激活码失效。用户拿新机器码来，维护者用该订单的私钥（从 `signing\orders\<OrderID>\` 取）签发新 LicenseID 的激活码即可（建议登记表里关联同一用户的旧 LicenseID，标注"换机重发"）。

### Q: 能不能加激活码有效期？

当前实现是**永久授权**（`Claims.Edition = "perpetual"`，`Validate()` 强制只接受 perpetual）。如需支持有效期（试用版、订阅版），需要修改 `internal/licensing/claims.go` 加 `ExpiresAt` 字段、新增 `NewExpiringClaims` 构造函数、修改 `Validate()` 校验过期、keygen `issue` 加 `-expires` 参数。详见 `ClipVault_Offline_Licensing_Hardening_Plan.md`。

### Q: 能不能吊销已签发的激活码？

**不能**。Ed25519 签名是纯数学验证，客户端离线，没有吊销列表（CRL）通道。如果担心个别用户滥用，只能通过法律或商业手段处理。如需吊销能力，需要重新设计成在线授权或带短期 token + 在线续期模型，与当前"永久离线买断"定位冲突。

### Q: 私钥文件本身加密了吗？

是。`generate-key` 输出的私钥文件是 `scrypt + AES-256-GCM` 加密的 JSON（scrypt 参数 N=32768, r=8, p=1，盐 16 字节，AAD = `clipvault-license-private-key-v1`）。即使私钥文件被偷，没有口令也无法解密。但口令文件和私钥文件**不要放在一起**，否则加密意义被削弱。

### Q: 一用户一密钥对会不会太麻烦？

每个订单只是多跑一次 `build-for-order.ps1`（约 30 秒），换来的是**隔离性**：某个私钥泄露只影响该用户，不需要全局轮换。对比"所有用户共用一个私钥"的方案，一旦私钥泄露，所有用户都需要换新 exe 重新激活。对于买断制永久授权的离线软件，一用户一密钥对是更稳妥的选择。

### Q: 能在项目目录里临时放私钥签发吗？

**可以**。`build-for-order.ps1` 默认就把密钥对生成在 `signing\orders\<OrderID>\` 下。真正的防线是：
1. `signing/.gitignore` 拦截 `*.ed25519`/`*.private-key`/`*passphrase*.txt`/`orders/`，即使误放在 `signing/` 下也不会进 Git；
2. 对外只分发编译后的 exe，不分发源码，所以项目目录里的私钥不会随产品流出。

## 相关文档

- [ClipVault_Offline_Licensing_Hardening_Plan.md](../ClipVault_Offline_Licensing_Hardening_Plan.md) — 授权系统设计与安全要求
- [ClipVault_完整发布测试步骤.md](../ClipVault_完整发布测试步骤.md) — 发布前完整验收清单
- [README.md](../README.md) — 项目总览与构建说明
- [scripts/build-release.ps1](../scripts/build-release.ps1) — Garble 混淆构建脚本（被 build-for-order.ps1 调用）
- [cmd/license-keygen/main.go](../cmd/license-keygen/main.go) — 底层 keygen 实现
- [internal/licensing/claims.go](../internal/licensing/claims.go) — Claims 结构与验签逻辑
