# ClipVault

ClipVault 是一个用 Go 编写的 Windows 剪贴板管理工具。后台托盘运行，所有历史写入**加密的 DuckDB 数据库**，并提供内置 Web 搜索与配置界面。数据库密钥通过 Windows DPAPI 保护，支持恢复密码备份与跨设备恢复。

## 功能

- **托盘后台运行**：右键菜单支持暂停/恢复、搜索历史/设置、打开数据目录、开机自启动、退出
- **全局快捷键**：`Ctrl+Alt+P` 暂停/恢复（可在 Web 设置页配置）
- **星标收藏**：点击卡片上的 ⭐ 按钮收藏条目，支持「收藏」筛选器快速查看
- **敏感内容规则**：通过正则表达式标记敏感内容；记录仍会加密保存，查看、复制、文件与条目变更操作需要二次验证
- **文件拖放记录**：复制文件时记录路径清单，按扩展名自动分类（图片/视频/压缩包/Office/其他）
- **哈希去重**：相同内容连续复制只保存一条
- **DuckDB 加密存储**：数据库使用 AES-256 加密，密钥由 DPAPI 保护
- **恢复密码**：首次启动必须设置（至少 12 个字符），用于备份数据库密钥；DPAPI 失效时（换电脑、重装系统）可恢复数据
- **锁屏密码 & 二次密码**：锁屏密码可选；二次密码首次启动必须设置且只能修改。两者均为 8–1024 个字符且首尾不能有空格
- **加密导出包**：支持 `.cvexport` 加密格式，用密码加密导出数据，安全传输或备份
- **定时备份**：目录和邮件备份支持 JSON、Excel 或加密 `.cvexport`；可在应用服务启动后自动执行一次，SMTP 强制 TLS
- **分组管理**：自定义分组对剪贴板记录分类，支持嵌套
- **便携模式**：exe 同目录放置 `portable.flag` 普通文件即可切换为便携模式，数据存放在 exe 目录下
- **日志按日期分文件**：默认写入 `%LOCALAPPDATA%\ClipVault\logs\YYYY-MM-DD.log`；便携模式写入 exe 同目录
- **诊断日志**：可导出最近 7 天、最多 8 MiB 的脱敏日志与运行环境信息，用于问题排查；不包含数据库、配置、密钥或剪贴板内容
- **永久离线授权**：机器码绑定的 Ed25519 激活码，一次激活后当前设备永久使用

### Web 界面（搜索 + 设置）

双击托盘图标或右键「搜索历史 / 设置」打开内置 Web 页（默认 `http://127.0.0.1:18765/`）：

#### 历史视图

| 功能 | 说明 |
|------|------|
| 实时搜索 | 输入即搜，180ms 防抖 |
| 关键词高亮 | 命中文本用橙色 `<mark>` 标注 |
| 类型过滤 | 按 `文本` / `图片` / `视频` / `压缩包` / `Office` / `其他文件` 分类筛选 |
| 收藏筛选 | 点击「收藏」chip，只显示已星标条目 |
| 日期范围筛选 | 按设置中的系统/IANA 时区选择起止日期过滤历史记录（结束日包含全天） |
| 分页 | 每页 50/100/200 条可选，显示当前页范围与总数 |
| 相对时间 | 显示「3 分钟前」，悬浮可看完整时间 |
| 一键复制 | 点击「复制」按钮，内容写入系统剪贴板并弹出 Toast 提示 |
| 弹窗查看 | 长文本点击「查看」按钮弹出全文预览，支持弹窗内复制，`Esc` 关闭 |
| 删除单条 | 点击「删除」按钮，卡片滑出动画后移除；敏感条目需要二次验证 |
| 清除全部 | 顶栏「清除」按钮，需二次确认 + 二次密码验证后删除所有记录 |
| 暂停/恢复 | 顶栏按钮一键切换录制状态 |
| 自动刷新 | 勾选后每 3 秒拉取最新记录 |
| 键盘快捷键 | `Ctrl+K` 聚焦搜索框，`Esc` 清空并失焦 / 关闭弹窗 |
| 深浅色主题 | 自动跟随系统 `prefers-color-scheme` |

#### 设置视图

| 分区 | 可配置项 |
|------|---------|
| 基本 | 最大文字长度（0 = 不限）、最大文件路径数（0 = 不限）、来源应用采集与展示 |
| 服务 | Web 监听地址、本机 HTTPS 反向代理、日期筛选时区；管理服务始终启动，避免配置死路 |
| 存储与诊断 | 查看并迁移数据库、密钥和应用数据（目标必须由用户提前创建、为空且可读写；诊断日志保留原位置）；导出最近 7 天、最多 8 MiB 的脱敏诊断日志 |
| 安全 | 锁屏密码、二次密码、数据恢复密码（必需，可修改；忘记旧密码时需刚刚完成二次认证后重置）、锁屏会话超时、二次认证缓存时长 |
| 授权 | 查看授权状态与机器码、复制购买信息、粘贴激活码并即时激活 |
| 过滤 | 自定义正则敏感规则，支持增删；匹配内容会标为敏感并继续写入加密数据库，查看和文件操作受二次认证保护 |
| 分组 | 创建/编辑/删除分组，支持嵌套，可将记录加入分组 |
| 导出 | 按全部或日期范围导出（JSON / Excel / 加密 .cvexport）、导入数据（JSON / Excel / .cvexport）；Excel 保留来源应用信息 |
| 备份 | 目录和邮件定时备份（可选启动后自动执行）、SMTP 与安全邮件模板、立即备份/发送、测试邮件、最近备份状态；邮件发送历史以只读弹窗展示最近 100 条成功记录（自动/手动、收发账号、时间、文件类型） |

> 过滤规则和文字限制**即时生效**；监听地址或反向代理配置变更需**重启应用**后生效。

> 端口被占时，自动尝试下 5 个端口（如 18765 → 18769），日志记录实际使用的端口。

### 局域网与 HTTPS 反向代理

- 直接局域网访问继续使用 HTTP。监听非回环地址前必须先设置锁屏密码；二次密码不是开启局域网的前置条件。
- ClipVault 不内置证书管理。需要 HTTPS 时，在同一台电脑上运行 Caddy、Nginx 等反向代理，ClipVault 后端保持监听 `127.0.0.1` 或其他回环地址。
- 在设置页启用「HTTPS 反向代理」并填写客户端实际访问的完整地址，例如 `https://clipvault.example.com`。地址不能包含路径、账号、查询参数或片段。
- ClipVault 只信任来自回环 TCP 连接的 `X-Forwarded-Proto: https` 和与配置地址完全匹配的 `X-Forwarded-Host`。其他来源的转发头会被拒绝；HTTPS 会话 Cookie 自动带 `Secure`。
- 本机直接访问 `http://127.0.0.1:18765` 仍然可用。该模式不增加自定义“接口加密层”，传输机密性由标准 HTTPS 负责。

Caddy 示例（代理与 ClipVault 必须在同一台电脑）：

```caddyfile
clipvault.example.com {
    reverse_proxy 127.0.0.1:18765 {
        header_up X-Forwarded-Proto https
        header_up X-Forwarded-Host {host}
    }
}
```

## 安全架构

```
用户密码 ──→ 恢复密码 (scrypt + AES-256-GCM) ──→ 加密 clipvault.recovery
                                                      │
Windows DPAPI ──→ 加密 clipvault.key ──→ db_key ────→ DuckDB AES-256 加密
                                                      │
                                               clipvault.duckdb (加密数据库)
```

- **db_key**：32 字节随机密钥，用于 DuckDB 数据库加密
- **clipvault.key**：db_key 经 DPAPI 加密后存储，绑定当前 Windows 用户
- **clipvault.recovery**：db_key 经用户恢复密码（scrypt + AES-256-GCM）加密后存储；首次启动与二次密码一并创建并自检
- **clipvault.state**：不含敏感数据的元数据文件，仅记录数据库引擎、密钥指纹和时间戳

### DPAPI 失效恢复

当 DPAPI 失效（换电脑、重装系统、用户配置损坏）时：

1. 应用启动检测到 `OpenStore` 失败，自动启动恢复 HTTP 服务
2. 浏览器打开恢复页面，用户输入恢复密码
3. 用恢复密码解密 `clipvault.recovery` 获取 db_key
4. 验证 db_key 能打开 DuckDB 数据库
5. 用新用户的 DPAPI 重新加密 db_key，写入 `clipvault.key`
6. 应用正常启动

恢复密码不是锁屏密码、二次密码、授权码或导出密码。如果 Windows DPAPI 密钥与恢复密码同时丢失，加密数据库无法恢复。数据库仍能通过 DPAPI 打开但用户忘记恢复密码时，可在设置页重新输入二次密码后重置恢复密码。

如果数据库中的配置 JSON 无法解析、缺少当前版本必需字段、含未知字段、版本不匹配或字段值无效，应用会 fail-closed：不会用默认配置静默关闭安全密码，也不会启动录制；用户输入恢复密码后，程序恢复最近一次成功保存的配置。配置与其最近有效快照均保存在已经加密的 DuckDB 中，不另建明文 JSON 配置文件。

### 开发阶段数据库

开发版只接受当前精确的 schema 和配置版本，不包含数据库升级代码。数据结构调整后，停止程序，确认目标是开发数据目录，删除 `clipvault.duckdb`、`clipvault.key`、`clipvault.recovery` 与 `clipvault.state`，然后重新启动并创建恢复密码和二次密码。

## 构建

> **前提**：需要 MinGW-w64（CGO 依赖，DuckDB 需要 C 编译器，`gcc --version` 必须可用）和 Go 1.25+。正式 Garble 发布构建需要 Go 1.26+

> **授权注入**：`internal/licensing/keys.go` 中 `builtinPublicKeyBase64` 默认为空字符串，未通过 `-ldflags -X` 注入公钥时，`BuiltinTrustedKeys()` 返回空 map，启动后 `Status=unconfigured` 且 `Enforced=true`，**录制、备份等付费功能全部禁用，也无法激活**。因此 `go run` / `go build` 开发调试时必须同时注入 `CLIPVAULT_LICENSE_KEY_ID` 和 `CLIPVAULT_LICENSE_PUBLIC_KEY`（与 `scripts/build-release.ps1` 同源）。

### 开发调试（推荐：用 dev-run 脚本）

`.\scripts\dev-run.ps1` 一条命令搞定全部：

1. 检查 `signing/cv-dev.ed25519` 调试密钥对是否存在，不存在则自动生成（口令文件也不存在时自动生成随机口令）
2. 用调试私钥给**当前机器**签发永久激活码，直接写入当前数据目录的 receipt 文件（遵循 `CLIPVAULT_DATA_DIR`、`portable.flag` 与设置页的数据目录配置）
3. 读调试公钥，通过 `-ldflags -X` 注入到 clipvault
4. `go run` 启动 clipvault（带控制台窗口，便于看日志）

启动后客户端就是**已激活状态**，不需要再手动粘激活码。

```powershell
# 设置 MinGW 路径（每次新开终端都需要，CGO 由脚本自动设置）
$env:PATH = "D:\mingw64\bin;" + $env:PATH

# 一键启动
.\scripts\dev-run.ps1

# 透传子命令，例如命令行搜索
.\scripts\dev-run.ps1 search 关键字
```

脚本默认用 `signing/cv-dev.ed25519` + `signing/cv-dev.ed25519.pub` + `signing/key-passphrase-dev.txt` 这套调试密钥（私钥和口令被 `.gitignore` 拦截不进 Git，公钥可公开）。也可以用环境变量覆盖：

| 参数 | 环境变量 | 默认值 |
|------|---------|--------|
| KeyID | `CLIPVAULT_LICENSE_KEY_ID` | `cv-main-2026` |
| 私钥路径 | — | `signing\cv-dev.ed25519` |
| 公钥路径 | — | `signing\cv-dev.ed25519.pub` |
| 口令文件 | — | `signing\key-passphrase-dev.txt` |

### 手动签发激活码（可选）

`dev-run.ps1` 已经自动签发并写入 receipt，通常不需要手动操作。如果想给**另一台机器**签发激活码（比如同事的电脑），用 `license-keygen`：

```powershell
# 1. 让对方运行 clipvault，从 Web 设置页「复制购买信息」拿到完整机器码（64 位十六进制）

# 2. 用调试私钥签发激活码
go run ./cmd/license-keygen issue `
  -key signing\cv-dev.ed25519 `
  -passphrase-file signing\key-passphrase-dev.txt `
  -license-id CV-DEV-000002 `
  -machine <对方完整机器码>
```

输出的 `CV1.xxx.yyy` 激活码粘到对方客户端「设置 → 授权」分区激活即可。

### 手动构建

```powershell
# 设置 CGO 和 MinGW 路径（每次新开终端都需要）
$env:CGO_ENABLED = 1
$env:PATH = "D:\mingw64\bin;" + $env:PATH

# 控制台版构建（开发调试用，需自行注入 ldflags 才能激活授权）
$KeyId = $env:CLIPVAULT_LICENSE_KEY_ID
$PublicKey = $env:CLIPVAULT_LICENSE_PUBLIC_KEY
$ldflags = "-X clipboard_ext/internal/licensing.builtinKeyID=$KeyId -X clipboard_ext/internal/licensing.builtinPublicKeyBase64=$PublicKey"
go build "-ldflags=$ldflags" -o bin\clipvault.exe .\cmd\clipvault

# GUI 版（无黑窗口，正式发布用 build-release.ps1）
go build "-ldflags=-H=windowsgui $ldflags" -o bin\clipvault.exe .\cmd\clipvault
```

> Windows 资源文件（`.syso`）已预生成在 `cmd/clipvault/` 目录下，包含图标和版本信息。修改图标后需用 `rsrc` 工具重新生成。

### 正式授权构建

所有构建均强制校验授权：未输入与当前机器码匹配的激活码前，录制功能不会启用。生产私钥只用于离线签发激活码，不能放入源码、构建机、安装包或 Git 仓库。

```powershell
# 在断网或专用签发机上创建加密私钥；口令文件至少 16 字节
go run ./cmd/license-keygen generate-key `
  -out D:\offline\clipvault.private-key `
  -passphrase-file D:\offline\key-passphrase.txt

# 根据用户界面复制出的完整机器码签发永久激活码
go run ./cmd/license-keygen issue `
  -key D:\offline\clipvault.private-key `
  -passphrase-file D:\offline\key-passphrase.txt `
  -license-id CV-2026-000001 `
  -machine 用户完整机器码

# 发布机只持有 .pub 公钥；脚本使用 Garble 混淆并输出 SHA-256
$env:CLIPVAULT_LICENSE_KEY_ID = "cv-main-2026"
$env:CLIPVAULT_LICENSE_PUBLIC_KEY = (Get-Content D:\offline\clipvault.private-key.pub -Raw).Trim()
$env:CLIPVAULT_GARBLE_SEED = "请使用长期保存的随机 Garble seed"
.\scripts\build-release.ps1
```

发布前还需要使用可信代码签名证书对 `dist\clipvault.exe` 做 Authenticode 签名，并在干净的 Windows 虚拟机验证安装、激活、升级和卸载流程。代码混淆只能增加分析成本，不能替代签名、私钥隔离和授权校验。

## 使用

```powershell
.\bin\clipvault.exe                     # 托盘后台运行
.\bin\clipvault.exe run                 # 同上
.\bin\clipvault.exe search 关键字       # 命令行搜索历史
.\bin\clipvault.exe data                # 打印数据目录路径
```

开机启动通过托盘菜单或 Web 设置页管理。

### 便携模式

在 exe 同目录下创建 `portable.flag` 空普通文件即可启用便携模式：

```powershell
New-Item -ItemType File "portable.flag"
.\bin\clipvault.exe
```

便携模式下数据存放在 `exe目录\data\`，日志在 `exe目录\logs\`。

### 数据目录优先级

1. `CLIPVAULT_DATA_DIR` 环境变量
2. `portable.flag` 普通文件存在 → `exe目录\data\`
3. 设置页已确认的自定义数据目录
4. `%LOCALAPPDATA%\ClipVault\`（默认）
5. `%APPDATA%\ClipVault\`（降级）

`CLIPVAULT_DATA_DIR` 必须使用绝对路径，避免启动程序与维护脚本解析到不同目录。

在设置页迁移数据目录时，目标目录必须由用户预先创建、保持为空，并同时通过读取、创建和删除文件及子目录的权限检测。迁移数据库、密钥和应用数据，诊断日志继续保留在原位置。自定义位置记录必须是绝对路径，且不接受符号链接或联接点。返回默认位置时，目录中仅有 ClipVault 自身的控制文件和日志也可使用。迁移任务在应用完全退出后的下一次启动前执行，避免复制运行中的数据库文件；程序不会自动创建或合并用户文件。

使用 `scripts/Rebuild-ClipVaultDatabase.ps1` 维护便携包时，若脚本不在 exe 同级目录的 `scripts` 中，请显式传入绝对路径的 `-DataDir <exe目录>\data`，避免操作错误的数据目录。脚本可重复执行：数据目录已经没有数据库或品牌资源时会成功返回，不会创建空的 `rebuild-backup-*`；当前 schema 和默认设置由 ClipVault 下次启动创建。

## Web API

后端采用模块化单体：`domain` 与 `application` 位于核心层，`storage/duckdb`、
`transfer`、`backup`、`security` 提供基础设施和纯策略，`transport/httpui` 与
`platform/windows` 负责外部适配，`bootstrap` 统一命令解析与生命周期装配。
架构测试会校验这些包真实存在、被适配层消费且不存在由核心层指向外层的反向依赖。

| 接口 | 方法 | 认证 | 说明 |
|------|------|------|------|
| `GET /` | GET | — | 搜索与设置页面 |
| `POST /api/v1/session/login` | POST | — | 锁屏密码登录 |
| `POST /api/v1/session/secondary` | POST | 锁屏（若已设置） | 二次密码验证 |
| `GET /api/v1/session/csrf` | GET | — | 获取本机页面的 CSRF token |
| `GET /api/v1/license` | GET | 锁屏 | 查询授权状态 |
| `GET /api/v1/license/machine` | GET | 锁屏 | 获取当前完整机器码 |
| `POST /api/v1/license/activation` | POST | 锁屏 + CSRF | 离线激活 |
| `GET /api/v1/items` | GET | 锁屏 | 搜索历史 |
| `GET /api/v1/runtime` | GET | 锁屏 | 返回运行状态 |
| `POST /api/v1/runtime/pause` | POST | 锁屏 + CSRF | 暂停/恢复 |
| `DELETE /api/v1/items?id=N` | DELETE | 锁屏；敏感条目另需二次 | 删除指定记录 |
| `PATCH /api/v1/items?id=N&starred=1` | PATCH | 锁屏；敏感条目另需二次 | 收藏/取消收藏 |
| `GET /api/v1/items/groups?item_id=N` | GET | 锁屏；敏感条目另需二次 | 获取指定记录所属分组 |
| `POST /api/v1/items/group` | POST | 锁屏 + CSRF；敏感条目另需二次 | 将指定记录加入分组 |
| `DELETE /api/v1/items/group?item_id=N&group_id=N` | DELETE | 锁屏 + CSRF；敏感条目另需二次 | 将指定记录移出分组 |
| `POST /api/v1/items/clear` | POST | 二次 + CSRF | 清除全部记录 |
| `GET /api/v1/config` | GET | 锁屏 | 获取配置 |
| `PUT /api/v1/config` | PUT | 二次 + CSRF | 保存配置 |
| `GET /api/v1/storage` | GET | 锁屏 | 查看当前数据目录与是否允许迁移 |
| `POST /api/v1/storage/relocation` | POST | 二次 + CSRF | 校验并登记数据目录迁移，退出并重新启动后执行 |
| `GET /api/v1/diagnostics` | GET | 二次 | 导出最近 7 天的脱敏诊断日志 ZIP |
| `GET /api/v1/transfer/export?format=json` | GET | 二次 | 导出记录（json/xlsx，支持 full/range/incremental） |
| `POST /api/v1/transfer/export?format=cvexport` | POST | 二次 + CSRF | 加密导出，密码放在 JSON 请求体 |
| `POST /api/v1/transfer/import` | POST | 二次 + CSRF | 导入数据（JSON / Excel / .cvexport；必须设置 `X-ClipVault-Import-Format`） |
| `GET /api/v1/recovery` | GET | 二次 | 查询恢复密码状态 |
| `POST /api/v1/recovery/password` | POST | 二次 + CSRF | 修改恢复密码 |
| `POST /api/v1/recovery/reset` | POST | 刚刚完成的二次认证 + CSRF | 忘记旧恢复密码时原子重置 |
| `GET /api/v1/backup` | GET | 锁屏 | 查看备份配置与最近状态 |
| `GET /api/v1/backup/email-history` | GET | 二次 | 只读查看最近 100 条成功发送的邮件备份历史 |
| `POST /api/v1/backup/directory` | POST | 二次 + CSRF | 立即执行目录备份 |
| `POST /api/v1/backup/email` | POST | 二次 + CSRF | 立即发送备份邮件 |
| `POST /api/v1/backup/email-test` | POST | 二次 + CSRF | 发送测试邮件 |
| `/api/v1/groups` | GET/POST/PUT/DELETE | 锁屏；变更请求需 CSRF | 列出、创建、更新和删除分组 |

导出范围统一使用 `full`、半开时间区间 `range`（`[from,to)`）或带正整数 `sinceId` 的 `incremental`；Web 页面输入的日期会按 `search.timeZone` 转换为 RFC 3339 时刻后再交给存储层。Excel 工作簿使用固定的 `Manifest`、`Records`、`Groups`、`ItemGroups`、`SensitiveRules` 五个工作表；`Manifest` 固定声明 `ClipVault` 和当前导出协议版本，其余工作表完整保存记录、来源程序名/路径/窗口标题/识别方法、敏感标记、分组及父子层级、记录分组关系和敏感规则。导入只接受当前协议版本，且不包含密码、恢复密钥、SMTP 凭据等秘密配置。

所有 API 成功响应使用 `{ "data": ... }`，错误统一使用 `{ "error": { "code": "...", "message": "..." } }`。所有 `POST`、`PUT`、`PATCH` 和 `DELETE` 请求均必须携带 `GET /api/v1/session/csrf` 返回的 `X-ClipVault-CSRF` 请求头；浏览器发起的变更请求还必须保持同源。上表的认证列表示在此基础上还需要的锁屏或二次认证。

**`/api/v1/items` 查询参数：**

| 参数 | 说明 |
|------|------|
| `q` | 搜索关键词（最多 8 个以空白分隔的词，总长度最多 4096 字节） |
| `limit` | 每页条数（默认 50，上限 500） |
| `offset` | 分页偏移 |
| `starred=1` | 只返回已收藏条目 |
| `kind` | 按类型筛选：`text` / `image` / `video` / `archive` / `office` / `files` |
| `date_from` | 起始日期（`YYYY-MM-DD`，含） |
| `date_to` | 结束日期（`YYYY-MM-DD`，含） |
| `group_id` | 按正整数分组 ID 筛选 |
| `source_exe` | 按来源程序名精确筛选；可重复，最多 50 个 |

`/api/v1/items/sensitive` 使用相同的分页、收藏、类型、日期、分组和来源筛选参数。格式错误或越界的数字参数会返回 HTTP 400，不会静默退化为默认查询。加密导入导出推荐通过内置 Web 页面操作。密码和二次认证 token 不接受 URL 查询参数，避免进入浏览器历史和日志。当前数据协议为 export v4，加密容器为 `.cvexport` v2（魔数 `CVEXP003`），只接受当前版本并严格校验 KDF、封装长度和解压上限。

## 配置说明

配置存储在 DuckDB schema v2 的 `settings` 表中，通过 Web 设置页面修改，也可通过 `PUT /api/v1/config` 接口写入。`PUT` 接收完整配置对象，拒绝缺失字段、未知字段、多余 JSON 值、错误版本和越界值；程序化调用必须先获取当前配置再整体回写。下方 JSON 只是核心字段说明摘录，不是可直接提交的完整 `PUT` 请求体。

```json
{
  "version": 1,
  "maxTextChars": 0,
  "maxFileDropPaths": 0,
  "search": {
    "listenAddr": "127.0.0.1:18765",
    "timeZone": "system",
    "reverseProxy": {
      "enabled": false,
      "publicUrl": ""
    }
  },
  "sensitive": {
    "customPatterns": []
  },
  "autostart": {
    "enabled": false,
    "appName": "ClipVault"
  },
  "security": {
    "lockSessionTimeout": 0,
    "secondaryAuthTimeout": 0
  },
  "backup": {
    "dirBackup": {
      "enabled": false,
      "runOnStartup": false,
      "interval": 24,
      "dir": "",
      "format": "cvexport",
      "password": ""
    },
    "emailBackup": {
      "enabled": false,
      "runOnStartup": false,
      "interval": 24,
      "smtpHost": "",
      "smtpPort": 587,
      "smtpUser": "",
      "smtpPass": "",
      "from": "",
      "to": "",
      "format": "cvexport",
      "password": "",
      "subject": "ClipVault 备份 - {time}",
      "body": "已生成 {format} 备份文件：{filename}",
      "style": "card",
      "accentColor": "#16A34A",
      "maxPartSize": 20971520
    }
  }
}
```

| 字段 | 说明 |
|------|------|
| `version` | 配置结构版本；必须与当前程序一致 |
| `maxTextChars` | 超过此字符数的文本直接跳过；`0` 表示不限制 |
| `maxFileDropPaths` | 文件拖放时最多记录的路径数；`0` 表示不限制 |
| `search.listenAddr` | Web 服务监听地址；默认仅 localhost/回环地址。开放局域网监听必须先设置锁屏密码并确认风险；端口被占时自动尝试下 5 个端口 |
| `search.timeZone` | 日期筛选和范围导出的时区；`system` 表示 Windows 当前时区，也可填 IANA 时区名 |
| `search.reverseProxy.enabled` | 是否信任同机回环反向代理传入的 HTTPS 主机信息；启用时必须设置锁屏密码且 `listenAddr` 必须保持为回环地址 |
| `search.reverseProxy.publicUrl` | 客户端实际访问的 HTTPS 根地址；主机和非默认端口必须与 `X-Forwarded-Host` 完全匹配 |
| `sensitive.customPatterns` | 自定义正则列表；匹配记录仍加密保存，搜索、查看和文件操作受二次认证保护 |
| `autostart.enabled` | 是否开机自启动 |
| `security.lockSessionTimeout` | 锁屏会话超时（分钟），0 = 关闭浏览器后失效 |
| `security.secondaryAuthTimeout` | 二次认证缓存时长（分钟），0 = 每次都校验 |
| `backup.dirBackup.enabled` | 是否启用定时目录备份 |
| `backup.dirBackup.runOnStartup` | 应用服务启动后是否自动执行一次目录备份 |
| `backup.dirBackup.interval` | 目录备份间隔（小时） |
| `backup.dirBackup.dir` | 备份文件保存目录 |
| `backup.dirBackup.format` | 备份格式：`json`（明文）/ `xlsx`（明文 Excel）/ `cvexport`（加密） |
| `backup.dirBackup.password` | cvexport 格式的加密密码 |
| `backup.emailBackup.enabled` | 是否启用定时邮件备份 |
| `backup.emailBackup.runOnStartup` | 应用服务启动后是否自动执行一次邮件备份 |
| `backup.emailBackup.interval` | 邮件备份间隔（小时） |
| `backup.emailBackup.smtpHost` | SMTP 服务器地址 |
| `backup.emailBackup.smtpPort` | SMTP 端口（通常 587 或 465） |
| `backup.emailBackup.smtpUser` | SMTP 用户名；填写时必须为完整邮箱，可信内网 relay 可与密码同时留空 |
| `backup.emailBackup.smtpPass` | SMTP 密码或授权码；与用户名同时填写或同时留空 |
| `backup.emailBackup.from` | 发件人邮箱 |
| `backup.emailBackup.to` | 收件人邮箱 |
| `backup.emailBackup.format` | 备份格式：`json`（明文）/ `xlsx`（明文 Excel）/ `cvexport`（加密） |
| `backup.emailBackup.password` | cvexport 格式的加密密码 |
| `backup.emailBackup.subject` | 邮件标题，支持 `{time}`、`{filename}`、`{format}` |
| `backup.emailBackup.body` | 邮件正文，支持 `{time}`、`{filename}`、`{format}` |
| `backup.emailBackup.style` | 邮件样式：`plain` / `card` / `accent` |
| `backup.emailBackup.accentColor` | 邮件主色，格式为 `#RRGGBB` |
| `backup.emailBackup.maxPartSize` | 每封邮件的预计 MIME 体积上限（1–50 MiB）；备份会按完整记录自适应分成多封，单条记录仍超限时明确报错 |

邮件备份使用加密连接：端口 `465` 使用 TLS，其他端口必须支持 STARTTLS，应用拒绝明文 SMTP。邮件标题和正文只接受纯文本模板，支持 `{time}`、`{filename}`、`{format}`，服务端会转义 HTML 并生成受控样式。仅成功提交的备份邮件会写入发送历史；测试邮件不会写入，历史没有删除接口。Web 管理服务是配置和恢复的唯一交互入口，无法启动时应用会明确退出，不会进入无法再启用的后台状态。

## 项目结构

```
clipboard_ext/
├── cmd/clipvault/              # 入口：CLI 命令分发
│   ├── main.go
│   ├── icon.ico                # 应用图标
│   ├── icon.png                # 主图标（256×256）
│   ├── icon16.png              # 16×16 小图标
│   ├── rsrc.rc                 # Windows 资源脚本（图标 + 版本信息）
│   ├── rsrc_windows_amd64.syso # 编译时链入的资源文件
│   └── rsrc_windows_386.syso   # 同上（32 位）
├── cmd/license-keygen/         # 离线密钥生成与永久激活码签发工具
├── cmd/dev-activate/           # 开发用：给当前机器签发激活码并写入 receipt
├── internal/clipvault/         # 核心逻辑
│   ├── app_windows.go          # Windows 启动流程 + 恢复入口
│   ├── auth.go                 # 锁屏密码 & 二次密码认证管理
│   ├── autostart_windows.go    # 开机启动（注册表）
│   ├── backup.go               # 定时备份服务（目录备份 + 邮件备份 + SMTP）
│   ├── clipboard_windows.go    # 剪贴板监听与读取、文件类型识别
│   ├── config.go               # 配置结构、默认值、环境路径解析
│   ├── config_manager.go       # 配置管理器（内存缓存 + 变更通知）
│   ├── cvexport.go             # 加密导出包（scrypt + AES-256-GCM + gzip）
│   ├── dpapi_windows.go        # Windows DPAPI 加解密
│   ├── filter.go               # 自定义正则过滤引擎
│   ├── keyfile.go              # DPAPI 密钥文件 + 恢复文件读写
│   ├── logger.go               # 按日期分文件日志
│   ├── recovery.go             # 恢复逻辑（密码恢复 + 新建数据库）
│   ├── recovery_password.go    # 恢复密码管理（设置/修改/安全重置/验证/强度检查）
│   ├── recovery_server.go      # DPAPI 失效时的恢复 HTTP 服务
│   ├── recorder.go             # 剪贴板事件处理与持久化
│   ├── search_server.go        # HTTP 服务器（API + 嵌入式前端）
│   ├── shell_windows.go        # Shell 操作（打开目录等）
│   ├── state.go                # 运行时状态（暂停/恢复）
│   ├── statefile.go            # 应用状态文件（引擎、密钥指纹、时间戳）
│   ├── store.go                # DuckDB 存储层（记录 + 配置 + 分组 + 导入导出）
│   ├── windows_runtime.go      # Win32 托盘/窗口/消息循环
│   └── web/                    # 前端静态资源（编译时嵌入）
│       ├── index.html
│       ├── style.css
│       └── script.js
├── internal/licensing/         # 机器码、Ed25519 验签、DPAPI 授权凭据
├── scripts/build-release.ps1   # Garble 正式发布构建与校验和
├── go.mod
└── go.sum
```

## 技术栈

- **Go 1.25** + CGO
- **DuckDB**（`github.com/duckdb/duckdb-go/v2`）— 加密列式数据库
- **Windows DPAPI** — 用户级密钥保护
- **scrypt + AES-256-GCM** — 恢复密码和加密导出包
- **Ed25519 + DPAPI** — 永久离线激活码验签与当前用户授权凭据保护
- **SMTP** — 定时邮件备份发送
- **Win32 API** — 系统托盘、剪贴板监听、全局热键
- **embed.FS** — 前端静态资源编译时嵌入
