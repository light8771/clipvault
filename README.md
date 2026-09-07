# ClipVault

ClipVault 是一个用 Go 编写的 Windows 剪贴板管理工具。后台托盘运行，将可用正文与无正文采集事件分开记录，所有历史写入**加密的 DuckDB 数据库**，并提供内置 Web 搜索与配置界面。数据库密钥通过 Windows DPAPI 保护，支持恢复密码备份与跨设备恢复。

## 功能

- **内容/事件分离**：成功读取的文本、图片和文件保存为正文记录；剪贴板清空、读取期间被覆盖、格式不支持或文本/图片超限时保存结构化采集事件。事件没有伪正文或空内容哈希，同类短时事件仅在查询层合并展示
- **图片捕获**：支持 Win+Shift+S 截图、浏览器 Copy Image、微信/QQ 图片（注册 PNG / CF_DIBV5 / CF_DIB / CF_BITMAP），统一转码为托管 PNG 保存，可在历史页内联预览并恢复到剪贴板
- **快速搜索弹窗**：`Ctrl+Shift+Space` 呼出桌面弹窗，打开即展示最近 100 条正文记录，并可隐藏与上一条正文完全相同的连续重复项；`Enter` 浏览器打开 / `Shift+Enter` 仅恢复到剪贴板 / `Ctrl+Enter` 恢复并粘贴到之前窗口，三个动作的快捷键可自定义；双击列表恒为恢复
- **托盘后台运行**：右键菜单支持暂停/恢复、搜索历史/设置、打开数据目录/日志目录/软件目录、开机自启动、退出
- **全局快捷键**：`Ctrl+Alt+P` 暂停/恢复、`Ctrl+Shift+Space` 搜索弹窗（均可在 Web 设置页配置）
- **星标收藏**：点击卡片上的 ⭐ 按钮收藏条目，支持「收藏」筛选器快速查看
- **敏感内容**：自定义正则规则在采集时自动标记；历史页也可对任意记录**手动标记/取消敏感**（取消需二次验证）。敏感记录仍加密保存，查看、复制、文件与条目变更操作需要二次验证
- **文件拖放记录**：复制文件时记录路径清单，按扩展名自动分类（图片/视频/压缩包/Office/其他）
- **DuckDB 加密存储**：数据库使用 AES-256 加密，密钥由 DPAPI 保护；事件时间以 Unix 毫秒存储（发生时间，非写入时间）
- **恢复密码**：首次启动必须设置（至少 12 个字符），用于备份数据库密钥；DPAPI 失效时（换电脑、重装系统）可恢复数据
- **锁屏密码 & 二次密码**：锁屏密码可选（首启门禁引导设置，可留空跳过）；二次密码首次启动必须设置且只能修改。两者均为 8–1024 个字符且首尾不能有空格
- **加密导出包**：支持 `.cvexport` 加密格式，用密码加密导出数据，安全传输或备份
- **定时备份**：目录与邮件备份**仅支持加密导出**——备份文件恒为 `.cvexport` 加密容器（JSON/Excel 内容格式先打进 zip 再整体加密），任何格式都必须设置加密密码；SMTP 强制 TLS
- **精确历史清理**：支持全部清理或按秒级时间范围清理，删除前可预览命中条数，预览与删除严格共用同一条件；可选保留收藏记录
- **分组管理**：自定义分组对剪贴板记录分类，支持嵌套
- **便携模式**：exe 同目录放置 `portable.flag` 普通文件即可切换为便携模式，数据存放在 exe 目录下
- **日志按日期分文件**：默认写入 `%LOCALAPPDATA%\ClipVault\logs\YYYY-MM-DD.log`，可在设置中更改日志目录（重启生效）；便携模式固定写入 exe 目录下的 `logs`
- **诊断日志**：可导出最近 7 天、最多 8 MiB 的脱敏日志与运行环境信息，用于问题排查；不包含数据库、配置、密钥或剪贴板内容
- **永久离线授权**：机器码绑定的 Ed25519 激活码，一次激活后当前设备永久使用。未激活（免费版）可浏览/检索当前正文或事件视图最近 100 条原始记录——先圈定窗口再过滤，不向更早记录补数量；事件视图在窗口内按同一规则合并展示
- **数据接口（Data API）**：可选启用的外部只读查询 API，独立监听 + Bearer Token 认证；白名单 DSL 查询、批量获取、内容下载；敏感数据按令牌显式授权（默认不可见），全量审计留痕
- **MCP 只读服务**：可选启用 Model Context Protocol 服务（Streamable HTTP + Bearer Token），7 个只读工具 + 6 个资源，供 AI 客户端检索剪贴板历史；与数据接口共享令牌治理与审计

### Web 界面（搜索 + 设置）

双击托盘图标或右键「搜索历史 / 设置」打开内置 Web 页（默认 `http://127.0.0.1:15678/`）：

#### 历史视图

| 功能 | 说明 |
|------|------|
| 实时搜索 | 输入即搜，180ms 防抖 |
| 关键词高亮 | 命中文本用橙色 `<mark>` 标注 |
| 类型过滤 | 按 `文本` / `图片` / `视频` / `压缩包` / `Office` / `其他文件` 分类筛选，也可单独查看采集事件 |
| 收藏筛选 | 点击「收藏」chip，只显示已星标条目 |
| 日期范围筛选 | 按设置中的系统/IANA 时区选择起止日期过滤历史记录（结束日包含全天） |
| 采集事件展示 | 以稳定事件代码展示剪贴板清空、读取时被覆盖、特殊格式、数据未就绪、剪贴板占用、解码失败和大小超限；相同短时事件合并显示出现次数，原始事件仍完整保留 |
| 剪贴板图片 | 托管图片内联缩略图，点击查看大图 |
| 分页 | 每页 20/50/100/200/500 条可选（10–500 可自定义），显示当前页范围与总数 |
| 相对时间 | 显示「3 分钟前」，悬浮可看完整时间 |
| 一键复制 | 点击「复制」按钮，内容写入系统剪贴板并弹出 Toast 提示 |
| 弹窗查看 | 长文本点击「查看」按钮弹出全文预览，支持弹窗内复制，`Esc` 关闭 |
| 手动敏感标记 | 点击盾牌按钮标记/取消敏感；取消标记需二次验证 |
| 删除单条 | 点击「删除」按钮，卡片滑出动画后移除；敏感条目需要二次验证 |
| 清理历史 | 「设置 → 存储与数据 → 历史清理」区块的「清理历史…」按钮打开清理弹窗：全部或秒级时间范围，实时预览命中条数，可选保留收藏；需二次密码验证 |
| 暂停/恢复 | 顶栏按钮一键切换录制状态 |
| 自动刷新 | 勾选后每 3 秒拉取最新记录 |
| 键盘快捷键 | `Ctrl+K` 聚焦搜索框，`Esc` 清空并失焦 / 关闭弹窗 |
| 深浅色主题 | 自动跟随系统 `prefers-color-scheme` |

#### 设置视图

| 分区 | 可配置项 |
|------|---------|
| 基本 | 最大文字长度（0 = 不限）、最大文件路径数（0 = 不限）、来源应用采集与展示 |
| 图片与弹窗 | 剪贴板图片捕获开关与单张大小上限；弹窗连续重复正文的默认隐藏开关；三个动作的快捷键（[Ctrl+][Shift+][Alt+]Enter，三者不能相同） |
| 服务 | Web 监听地址、日期筛选时区；管理服务始终启动，避免配置死路 |
| 数据接口 | 启用/停用外部数据 API、监听地址；令牌创建/重生成/撤销/敏感授权（需二次认证）、审计查询与 CSV 导出、清空 |
| MCP 服务 | 启用/停用 MCP 只读服务、监听地址；令牌管理与数据接口一致；工具/资源能力清单展示、接入 JSON 示例复制、审计查询与 CSV 导出 |
| 存储与诊断 | 查看并迁移数据库、密钥和应用数据（目标必须由用户提前创建、为空且可读写）；查看或更改日志目录（重启生效，旧日志不搬迁）；导出最近 7 天、最多 8 MiB 的脱敏诊断日志 |
| 安全 | 锁屏密码、二次密码、数据恢复密码（必需，可修改；忘记旧密码时需刚刚完成二次认证后重置）、锁屏会话超时、二次认证缓存时长 |
| 授权 | 查看授权状态与机器码、复制购买信息、粘贴激活码并即时激活 |
| 过滤 | 自定义正则敏感规则，支持增删；匹配内容会标为敏感并继续写入加密数据库，查看和文件操作受二次认证保护 |
| 分组 | 创建/编辑/删除分组，支持嵌套，可将记录加入分组 |
| 导出 | 按全部或日期范围导出（JSON / Excel / 加密 .cvexport）、导入数据（JSON / Excel / .cvexport，自动识别 zip 加密打包载荷）；Excel 保留来源应用信息 |
| 备份 | 目录和邮件定时备份（可选启动后自动执行）、SMTP 与安全邮件模板、立即备份/发送（异步执行，状态自动刷新）、测试邮件、最近备份状态；目录/邮件备份历史以只读弹窗展示最近 100 条记录（成功/失败/跳过、触发方式、时间、格式、条数） |

> 过滤规则和文字限制**即时生效**；监听地址变更需**重启应用**后生效。

> 端口被占时，自动尝试下 5 个端口（如 15678 → 15682），日志记录实际使用的端口。

### 局域网访问

- ClipVault 定位为本机 / 局域网数据 API，仅提供明文 HTTP。监听非回环地址前必须先设置锁屏密码；二次密码不是开启局域网的前置条件。
- 不内置 HTTPS、证书管理或反向代理支持；`X-Forwarded-*` 请求头不会被特殊处理。如需公网访问，请自行使用 Caddy、Nginx 等做反向代理映射（证书与 TLS 配置由代理方负责），并保持监听地址为回环地址。

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

> **前提**：需要 MinGW-w64（CGO 依赖，DuckDB 需要 C 编译器，`gcc --version` 必须可用）和 Go 1.26.6+。

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

### 隔离 UI 预览（isolated-preview）

`cmd/isolated-preview` 在随机临时目录中启动一份完整的 Web UI（数据接口、MCP、备份服务全部接线），不触碰本机真实数据，适合 UI / E2E 调试：

```powershell
# 127.0.0.1:18795 起服务，Ctrl+C 退出并自动删除临时目录
go run ./cmd/isolated-preview

# 自动化测试推荐：到期自动优雅退出，defer 清理正常执行
go run ./cmd/isolated-preview -lifetime 10m
```

> **勿用 `taskkill /F` 强杀**：强杀走 TerminateProcess，跳过 Go 的 defer 清理，会在 `%TEMP%` 残留 `clipvault-ui-preview-*` 数据目录。工具下次启动时会自动清扫修改时间超过 1 小时的残留目录，但测试脚本仍应优先使用 `-lifetime` 或 Ctrl+C 优雅退出。

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
| `POST /api/v1/session/logout` | POST | 锁屏 + CSRF | 销毁当前会话 |
| `GET /api/v1/session` | GET | 锁屏 | 查询当前认证状态 |
| `POST /api/v1/session/secondary` | POST | 锁屏（若已设置） | 二次密码验证 |
| `GET /api/v1/session/csrf` | GET | — | 获取本机页面的 CSRF token |
| `POST /api/v1/session/password` | POST | 锁屏 + CSRF | 设置、修改或删除锁屏密码，以及修改二次密码 |
| `GET /api/v1/appearance` | GET | — | 认证前可读的公开视觉设置（主题与品牌外观引导） |
| `PUT/DELETE /api/v1/appearance/logo` | PUT/DELETE | 二次 + CSRF | 上传/重置品牌 Logo |
| `PUT/DELETE /api/v1/appearance/favicon` | PUT/DELETE | 二次 + CSRF | 上传/重置站点图标 |
| `PUT/DELETE /api/v1/appearance/background` | PUT/DELETE | 二次 + CSRF | 上传/重置背景媒体 |
| `POST /api/v1/system/restart` | POST | 锁屏 + CSRF | 触发应用重启 |
| `GET /api/v1/system/info` | GET | 锁屏 | 设置页运行信息（版本、运行时长、记录总数、数据库体积、数据目录、录制状态） |
| `GET /api/v1/license` | GET | 锁屏 | 查询授权状态 |
| `GET /api/v1/license/machine` | GET | 锁屏 | 获取当前完整机器码 |
| `POST /api/v1/license/activation` | POST | 锁屏 + CSRF | 离线激活 |
| `GET /api/v1/items` | GET | 锁屏 | 搜索历史 |
| `GET /api/v1/items/sources` | GET | 锁屏 | 来源程序统计（免费版只统计可见窗口） |
| `GET /api/v1/items/position?id=N` | GET | 锁屏 | 定位记录在时间线中的位置（免费版只统计可见窗口） |
| `GET /api/v1/items/sensitive` | GET | 二次 | 仅搜索敏感记录（复用搜索筛选参数） |
| `GET /api/v1/items/sensitive/item?id=N` | GET | 二次 | 读取单条敏感记录明文 |
| `GET /api/v1/items/image?id=N` | GET | 锁屏；敏感条目另需二次 | 内联返回托管剪贴板图片（PNG） |
| `GET /api/v1/files/check` | GET | 锁屏；敏感路径另需二次 | 检查记录的文件是否存在并返回元数据 |
| `GET /api/v1/files/content` | GET | 锁屏；敏感路径另需二次 | 内联查看或下载记录的文件 |
| `GET /api/v1/files/hash` | GET | 锁屏；敏感路径另需二次 | 按需计算并返回文件的 MD5 与 SHA-256 |
| `POST /api/v1/files/open-directory` | POST | 锁屏 + CSRF；敏感路径另需二次 | 在资源管理器中打开文件所在目录 |
| `GET /api/v1/runtime` | GET | 锁屏 | 返回运行状态 |
| `POST /api/v1/runtime/pause` | POST | 锁屏 + CSRF | 暂停/恢复 |
| `DELETE /api/v1/items?id=N` | DELETE | 锁屏；敏感条目另需二次 | 删除指定记录 |
| `PATCH /api/v1/items?id=N&starred=1` | PATCH | 锁屏；敏感条目另需二次 | 收藏/取消收藏 |
| `PATCH /api/v1/items/sensitive?id=N&sensitive=1` | PATCH | 锁屏；取消标记另需二次 | 手动标记/取消敏感 |
| `POST /api/v1/items/cleanup-preview` | POST | 二次 + CSRF | 预览清理将删除的记录数（不执行删除） |
| `POST /api/v1/items/cleanup` | POST | 二次 + CSRF | 按与预览完全相同的条件执行清理；支持全部/秒级范围，可保留收藏 |
| `GET /api/v1/items/groups?item_id=N` | GET | 锁屏；敏感条目另需二次 | 获取指定记录所属分组 |
| `POST /api/v1/items/group` | POST | 锁屏 + CSRF；敏感条目另需二次 | 将指定记录加入分组 |
| `DELETE /api/v1/items/group?item_id=N&group_id=N` | DELETE | 锁屏 + CSRF；敏感条目另需二次 | 将指定记录移出分组 |
| `GET /api/v1/config` | GET | 锁屏 | 获取配置 |
| `PUT /api/v1/config` | PUT | 二次 + CSRF | 保存配置 |
| `GET /api/v1/config/sensitive-rules` | GET | 二次 | 读取真实敏感过滤规则 |
| `GET /api/v1/storage` | GET | 锁屏 | 查看当前数据目录与是否允许迁移 |
| `POST /api/v1/storage/relocation` | POST | 二次 + CSRF | 校验并登记数据目录迁移，退出并重新启动后执行 |
| `POST /api/v1/storage/open` | POST | 锁屏 + CSRF | 在资源管理器中打开当前数据目录 |
| `GET /api/v1/diagnostics` | GET | 二次 | 导出最近 7 天的脱敏诊断日志 ZIP |
| `GET /api/v1/transfer/export?format=json` | GET | 二次 | 导出记录（json/xlsx，支持 full/range/incremental） |
| `POST /api/v1/transfer/export?format=cvexport` | POST | 二次 + CSRF | 加密导出，密码放在 JSON 请求体 |
| `POST /api/v1/transfer/import` | POST | 二次 + CSRF | 导入数据（JSON / Excel / .cvexport，自动识别 zip 载荷；必须设置 `X-ClipVault-Import-Format`） |
| `GET /api/v1/recovery` | GET | 二次 | 查询恢复密码状态 |
| `POST /api/v1/recovery/password` | POST | 二次 + CSRF | 修改恢复密码 |
| `POST /api/v1/recovery/reset` | POST | 刚刚完成的二次认证 + CSRF | 忘记旧恢复密码时原子重置 |
| `POST /api/v1/recovery/isolated-switch` | POST | 刚刚完成的二次认证 + CSRF | 登记切换到隔离数据库（退出后下次启动生效） |
| `DELETE /api/v1/recovery/isolated-switch` | DELETE | 刚刚完成的二次认证 + CSRF | 取消待执行的数据库切换 |
| `GET /api/v1/backup` | GET | 锁屏 | 查看备份配置与最近状态 |
| `GET /api/v1/backup/history?kind=dir\|email` | GET | 二次 | 只读查看指定通道最近 100 条备份历史（成功/失败/跳过） |
| `POST /api/v1/backup/directory` | POST | 二次 + CSRF | 异步启动一次目录备份（`?forceFull=1` 强制全量） |
| `POST /api/v1/backup/email` | POST | 二次 + CSRF | 异步启动一次邮件备份（`?forceFull=1` 强制全量） |
| `POST /api/v1/backup/email-test` | POST | 二次 + CSRF | 发送测试邮件 |
| `/api/v1/groups` | GET/POST/PUT/DELETE | 锁屏；变更请求需 CSRF | 列出、创建、更新和删除分组 |
| `GET /api/v1/logs/config-changes` | GET | 锁屏 | 分页查看系统设置修改记录（含新旧值，最多保留 1000 次） |
| `DELETE /api/v1/logs/config-changes` | DELETE | 二次 + CSRF | 清空设置修改记录 |
| `GET /api/v1/logs/searches` | GET | 锁屏 | 分页查看剪贴板搜索历史（最多保留 1000 条） |
| `DELETE /api/v1/logs/searches` | DELETE | 二次 + CSRF | 清空搜索历史 |

数据接口与 MCP 服务的管理端点（`/api/v1/data-api/*` 与 `/api/v1/mcp/*`，各 10 条）见下方「数据接口」与「MCP 只读服务」章节。

导出范围统一使用 `full`、半开时间区间 `range`（`[from,to)`）或带正整数 `sinceId` 的 `incremental`；Web 页面输入的日期会按 `search.timeZone` 转换为 RFC 3339 时刻后再交给存储层。Excel 工作簿使用固定的 `Manifest`、`Records`、`Groups`、`ItemGroups`、`SensitiveRules`、`Assets` 六个工作表；托管 PNG 以独立二进制条目存在 XLSX 包内，`Assets` 记录条目、长度和哈希的强绑定。`Manifest` 固定声明 `ClipVault` 和当前导出协议版本，其余工作表完整保存正文/事件判别类型、内容哈希或事件代码与指纹、采集阶段与格式集合、来源程序名/路径/窗口标题/归因方式、敏感标记、分组及父子层级、记录分组关系和敏感规则。JSON / cvexport 的 `assets` 字段同样完整携带图片字节。导入只接受当前协议版本，且不包含密码、恢复密钥、SMTP 凭据等秘密配置。

所有 API 成功响应使用 `{ "data": ... }`，错误统一使用 `{ "error": { "code": "...", "message": "..." } }`。所有 `POST`、`PUT`、`PATCH` 和 `DELETE` 请求均必须携带 `GET /api/v1/session/csrf` 返回的 `X-ClipVault-CSRF` 请求头；浏览器发起的变更请求还必须保持同源。上表的认证列表示在此基础上还需要的锁屏或二次认证。

**`/api/v1/items` 查询参数：**

| 参数 | 说明 |
|------|------|
| `q` | 搜索关键词（最多 8 个以空白分隔的词，总长度最多 4096 字节） |
| `limit` | 每页条数（默认 50，上限 500） |
| `offset` | 分页偏移 |
| `starred=1` | 只返回已收藏条目 |
| `kind` | 按类型筛选：`text` / `image` / `video` / `archive` / `office` / `files` |
| `record_type` | 记录投影：默认或 `content` 为正文，`event` 为采集事件，`all` 为未分组的全部原始记录 |
| `date_from` | 起始日期（`YYYY-MM-DD`，含） |
| `date_to` | 结束日期（`YYYY-MM-DD`，含） |
| `group_id` | 按正整数分组 ID 筛选 |
| `source_exe` | 按来源程序名精确筛选；可重复，最多 50 个 |

`/api/v1/items/sensitive` 使用相同的分页、收藏、类型、日期、分组和来源筛选参数，但只查询正文。格式错误或越界的数字参数会返回 HTTP 400，不会静默退化为默认查询。加密导入导出推荐通过内置 Web 页面操作。密码和二次认证 token 不接受 URL 查询参数，避免进入浏览器历史和日志。当前数据协议为 export v9，加密容器为 `.cvexport` v2（魔数 `CVEXP003`），只接受当前版本并严格校验 KDF、封装长度和解压上限。

## 数据接口（Data API）

可选启用的**外部只读数据 API**，独立于 Web 界面单独监听（默认 `127.0.0.1:17839`，默认关闭；启用与令牌管理在 Web 设置页「数据接口」分区，配置立即生效无需重启）。所有数据端点要求 `Authorization: Bearer <token>` 认证，令牌通过 Web 设置页创建，`cv_` 前缀，明文只在创建时显示一次，数据库只保存 SHA-256 哈希。

### 外部端点（独立监听地址）

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/v1/health` | GET | 健康检查（无需令牌） |
| `/api/v1/metadata` | GET | 数据集元数据 |
| `/api/v1/query` | POST | 结构化查询：`filter` / `orderBy` / `limit` / `cursor` 游标分页 / `groupBy` / `aggregate` |
| `/api/v1/items/{id}` | GET | 按 ID 获取单条记录 |
| `/api/v1/items/batch-get` | POST | 批量获取（最多 100 个 ID） |
| `/api/v1/items/{id}/content` | GET | 获取记录内容（文本或托管 PNG） |

查询 DSL 为白名单严格校验的子集：过滤字段仅 `id`、`createdAt`、`kind`、`text`、`sourceApp`、`sourceWindow`、`starred`、`sensitive`、`groupId`；`groupBy` 仅 `sourceApp` / `kind` / `starred` / `sensitive`。SQL 全参数化，格式错误的请求返回 400，不静默退化。

请求体示例（`resource` 必填，`filter` 为「字段 → 操作符 → 值」对象结构；字符串字段支持 `eq`/`neq`/`contains`/`startsWith`/`endsWith`/`in`，时间字段支持 `gte`/`gt`/`lte`/`lt`，值为 RFC 3339 时刻）：

```json
{
  "resource": "clipboardItems",
  "filter": { "text": { "contains": "ClipVault" }, "createdAt": { "gte": "2026-08-01T00:00:00Z" } },
  "orderBy": "createdAt",
  "order": "desc",
  "limit": 50,
  "cursor": ""
}
```

**注意**：数据接口是独立契约，响应为裸对象（**无主 Web API 的 `data` 包装**）：列表查询返回 `{"items":[...],"nextCursor":"...","hasMore":true}`，聚合查询返回 `{"rows":[...]}`；错误统一返回 `{"error":"...","message":"..."}`。

### 管理端点（主 Web 服务 `/api/v1/data-api/*`）

| 接口 | 方法 | 认证 | 说明 |
|------|------|------|------|
| `/api/v1/data-api` | GET | 锁屏 | 服务状态：启用、监听地址、运行状态、URL、令牌数量 |
| `/api/v1/data-api/config` | PUT | 二次 + CSRF | 更新服务配置（立即生效） |
| `/api/v1/data-api/tokens` | GET | 锁屏 | 令牌列表（不含明文） |
| `/api/v1/data-api/tokens` | POST | 二次 + CSRF | 创建令牌（`expiresIn` 必填：`1d`/`7d`/`30d`/`90d`/`1y`/`never`；空值或不支持的值拒绝，防止疏忽时静默签出永不过期令牌；明文只返回一次） |
| `/api/v1/data-api/tokens/regenerate` | POST | 二次 + CSRF | 重新生成令牌（不继承敏感授权） |
| `/api/v1/data-api/tokens/revoke` | POST | 二次 + CSRF | 撤销令牌 |
| `/api/v1/data-api/tokens/sensitive` | POST | 二次 + CSRF | 调整令牌敏感数据授权（需重新二次认证） |
| `/api/v1/data-api/audit` | GET | 锁屏 | 分页查询审计（支持按操作/令牌过滤） |
| `/api/v1/data-api/audit/export` | GET | 锁屏 | 按当前过滤条件导出全部审计为 CSV（UTF-8 BOM） |
| `/api/v1/data-api/audit` | DELETE | 二次 + CSRF | 清空审计 |

### 敏感数据隔离

- 新令牌默认 `allowSensitive=false`：敏感记录被 SQL 下推过滤，**与不存在记录不可区分**（同样不出现或 404）
- 调整 `allowSensitive` 与重新生成令牌均需要重新完成二次认证；重新生成不继承敏感授权
- 每次请求写入审计（`api_audit_log`，保留 1000 条）：通道、令牌、来源 IP、方法、端点、操作、资源 URI、返回条数、耗时、状态码、请求摘要、是否访问敏感数据

## MCP 只读服务

可选启用的 **Model Context Protocol 只读服务**（Streamable HTTP，端点 `/mcp`，默认 `127.0.0.1:17840`，默认关闭；启用与令牌管理在 Web 设置页「MCP 服务」分区，配置立即生效无需重启）。认证与数据接口一致：`Authorization: Bearer <token>`，且令牌通道必须为 `mcp`（与 HTTP 数据接口通道隔离，跨通道使用返回 401）。

AI 客户端接入示例（Claude Desktop 等 MCP 客户端）：

```json
{
  "mcpServers": {
    "clipvault": {
      "url": "http://127.0.0.1:17840/mcp",
      "headers": {
        "Authorization": "Bearer cv_你的MCP令牌"
      }
    }
  }
}
```

### 工具（7 个，全部只读）

| 工具 | 说明 |
|------|------|
| `search_clipboard` | 按关键词、时间范围、来源应用搜索历史记录 |
| `get_clipboard_item` | 按 ID 获取单条记录完整元数据（ID 为十进制字符串） |
| `get_clipboard_items` | 批量获取（≤100，保持请求顺序，未授权 ID 静默省略） |
| `get_clipboard_content` | 获取记录内容：文本或托管 PNG 图片 |
| `count_clipboard_items` | 按过滤条件计数 |
| `get_clipboard_stats` | 结构化统计（总量、字节、按类型、按来源应用、时间趋势） |
| `list_clipboard_groups` | 分组列表（含可见条数） |

### 资源（6 个）

| 资源 URI | 说明 |
|----------|------|
| `clipvault://items/{id}` | 单条记录元数据 |
| `clipvault://items/{id}/content` | 记录内容 |
| `clipvault://recent` | 最近 50 条 |
| `clipvault://starred` | 最近收藏 50 条 |
| `clipvault://groups/{id}` | 分组详情 |
| `clipvault://groups/{id}/items` | 分组内最近 50 条 |

### 治理与审计

与数据接口 HTTP 治理口径一致：并发上限、查询超时、批量上限、内容大小上限与速率限制共享同一组参数（过载时低优先级请求先被拒绝）。敏感可见性规则相同：未授权令牌下敏感条目与不存在条目不可区分（`-32002 Resource not found`）。工具与资源调用均写审计（操作为 `tool:<name>` / `resource:<uri>`，含敏感访问标记），管理端点与数据接口对称：

| 接口 | 方法 | 认证 | 说明 |
|------|------|------|------|
| `/api/v1/mcp` | GET | 锁屏 | 服务状态与能力清单（工具/资源模板） |
| `/api/v1/mcp/config` | PUT | 二次 + CSRF | 更新服务配置（立即生效） |
| `/api/v1/mcp/tokens` | GET | 锁屏 | 令牌列表（不含明文） |
| `/api/v1/mcp/tokens` | POST | 二次 + CSRF | 创建令牌（`expiresIn` 必填，取值与数据接口一致：`1d`/`7d`/`30d`/`90d`/`1y`/`never`；空值或不支持的值拒绝） |
| `/api/v1/mcp/tokens/regenerate` | POST | 二次 + CSRF | 重新生成令牌（不继承敏感授权） |
| `/api/v1/mcp/tokens/revoke` | POST | 二次 + CSRF | 撤销令牌 |
| `/api/v1/mcp/tokens/sensitive` | POST | 二次 + CSRF | 调整令牌敏感数据授权（需重新二次认证） |
| `/api/v1/mcp/audit` | GET | 锁屏 | 分页查询审计（支持按操作/令牌过滤） |
| `/api/v1/mcp/audit/export` | GET | 锁屏 | 按当前过滤条件导出全部审计为 CSV（UTF-8 BOM） |
| `/api/v1/mcp/audit` | DELETE | 二次 + CSRF | 清空审计 |

> 两项服务默认仅监听回环地址。监听地址不能与 Web 服务或彼此冲突（保存时校验）；开放局域网监听前请先设置锁屏密码并确认令牌敏感授权范围。



配置存储在 DuckDB schema v11 的 `settings` 表中，通过 Web 设置页面修改，也可通过 `PUT /api/v1/config` 接口写入。`PUT` 接收完整配置对象，拒绝缺失字段、未知字段、多余 JSON 值、错误版本和越界值；程序化调用必须先获取当前配置再整体回写。下方 JSON 只是核心字段说明摘录，不是可直接提交的完整 `PUT` 请求体。

```json
{
  "version": 2,
  "maxTextChars": 0,
  "maxFileDropPaths": 0,
  "search": {
    "listenAddr": "127.0.0.1:15678",
    "timeZone": "system"
  },
  "sensitive": {
    "customPatterns": []
  },
  "image": {
    "enabled": true,
    "maxBytes": 33554432
  },
  "popup": {
    "hideConsecutiveDuplicates": false,
    "keyOpen": "Enter",
    "keyRestore": "Shift+Enter",
    "keyPaste": "Ctrl+Enter"
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
      "password": "",
      "scope": { "mode": "full" }
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
      "scope": { "mode": "full" },
      "subject": "ClipVault 备份 - {time}",
      "body": "已生成 {format} 备份文件：{filename}",
      "style": "card",
      "accentColor": "#16A34A",
      "maxPartSize": 20971520
    }
  },
  "dataApi": {
    "enabled": false,
    "listenAddr": "127.0.0.1:17839"
  },
  "mcp": {
    "enabled": false,
    "listenAddr": "127.0.0.1:17840"
  }
}
```

| 字段 | 说明 |
|------|------|
| `version` | 配置结构版本；必须与当前程序一致 |
| `maxTextChars` | 超过此字符数的文本保存为独立采集事件（不截断保存内容）；`0` 表示不限制 |
| `maxFileDropPaths` | 文件拖放时最多记录的路径数；`0` 表示不限制 |
| `search.listenAddr` | Web 服务监听地址；默认仅 localhost/回环地址，端口范围 1024–65535。开放局域网监听必须先设置锁屏密码并确认风险；修改地址时端口被占用会拒绝保存，启动时端口被占自动尝试下 5 个端口 |
| `search.timeZone` | 日期筛选和范围导出的时区；`system` 表示 Windows 当前时区，也可填 IANA 时区名 |
| `sensitive.customPatterns` | 自定义正则列表；匹配记录仍加密保存，搜索、查看和文件操作受二次认证保护。规则只在采集时生效，之后记录的敏感属性完全由用户手动控制 |
| `image.enabled` | 是否捕获剪贴板图片（截图/浏览器 Copy Image 等），统一转码为托管 PNG |
| `image.maxBytes` | 单张图片（转码后 PNG）的最大字节数，默认 32 MiB；超限保存为独立采集事件 |
| `popup.hideConsecutiveDuplicates` | 快速搜索弹窗是否默认隐藏与上一条正文的类型、格式和内容哈希都相同的记录；弹窗内可临时切换 |
| `popup.keyOpen` / `keyRestore` / `keyPaste` | 弹窗三个动作的快捷键：`[Ctrl+][Shift+][Alt+]Enter`，三者不能相同；双击列表恒为恢复 |
| `autostart.enabled` | 是否开机自启动 |
| `security.lockSessionTimeout` | 锁屏会话超时（分钟），0 = 关闭浏览器后失效 |
| `security.secondaryAuthTimeout` | 二次认证缓存时长（分钟），0 = 关闭浏览器后失效 |
| `backup.dirBackup.enabled` | 是否启用定时目录备份 |
| `backup.dirBackup.runOnStartup` | 应用服务启动后是否自动执行一次目录备份 |
| `backup.dirBackup.interval` | 目录备份间隔（小时） |
| `backup.dirBackup.dir` | 备份文件保存目录 |
| `backup.dirBackup.format` | 备份内容格式：`json` / `xlsx` / `cvexport`。**备份文件恒为 `.cvexport` 加密容器**：json/xlsx 的明文内容先打进 zip 再整体加密，任何格式都必须设置加密密码 |
| `backup.dirBackup.password` | 备份加密密码（**任何格式都必填**，仅支持加密导出） |
| `backup.dirBackup.scope` | 备份范围对象 `{mode, from, to, sinceId}`：`mode` 为 `full` / `incremental`（配 `sinceId`）/ `range`（配 `from`/`to`，半开区间 `[from,to)`，按 `search.timeZone` 解析） |
| `backup.dirBackup.fullFallbackDays` | 增量备份时，距上次全量超过该天数自动做一次全量兜底（0–365，默认 7，0=不自动兜底），防止已删除记录永远无法找回 |
| `backup.emailBackup.enabled` | 是否启用定时邮件备份 |
| `backup.emailBackup.runOnStartup` | 应用服务启动后是否自动执行一次邮件备份 |
| `backup.emailBackup.interval` | 邮件备份间隔（小时） |
| `backup.emailBackup.smtpHost` | SMTP 服务器地址 |
| `backup.emailBackup.smtpPort` | SMTP 端口（通常 587 或 465） |
| `backup.emailBackup.smtpUser` | SMTP 用户名；填写时必须为完整邮箱，可信内网 relay 可与密码同时留空 |
| `backup.emailBackup.smtpPass` | SMTP 密码或授权码；与用户名同时填写或同时留空 |
| `backup.emailBackup.from` | 发件人邮箱 |
| `backup.emailBackup.to` | 收件人邮箱 |
| `backup.emailBackup.format` | 备份格式：`json` / `xlsx` / `cvexport`。**附件恒为 `.cvexport` 加密容器**：json/xlsx 的明文文件先打进 zip 再整体加密 |
| `backup.emailBackup.password` | 邮件备份的加密密码（**任何格式都必填**，仅支持加密导出） |
| `backup.emailBackup.subject` | 邮件标题，支持 `{time}`、`{filename}`、`{format}` |
| `backup.emailBackup.body` | 邮件正文，支持 `{time}`、`{filename}`、`{format}` |
| `backup.emailBackup.style` | 邮件样式：`plain` / `card` / `accent` |
| `backup.emailBackup.accentColor` | 邮件主色，格式为 `#RRGGBB` |
| `backup.emailBackup.maxPartSize` | 每封邮件的预计 MIME 体积上限（1–50 MiB）；备份会按完整记录自适应分成多封，单条记录仍超限时明确报错 |
| `backup.emailBackup.scope` | 备份范围对象（结构与 `dirBackup.scope` 相同）：`full` / `incremental`（配 `sinceId`）/ `range`（配 `from`/`to`） |
| `backup.emailBackup.fullFallbackDays` | 增量备份时，距上次全量超过该天数自动做一次全量兜底（0–365，默认 7，0=不自动兜底） |
| `dataApi.enabled` | 是否启用外部数据接口（默认关闭） |
| `dataApi.listenAddr` | 数据接口监听地址（默认 `127.0.0.1:17839`）；不能与 Web 或 MCP 监听地址冲突，配置立即生效无需重启 |
| `mcp.enabled` | 是否启用 MCP 只读服务（默认关闭） |
| `mcp.listenAddr` | MCP 服务监听地址（默认 `127.0.0.1:17840`）；不能与 Web 或数据接口监听地址冲突，配置立即生效无需重启 |

邮件备份使用加密连接：端口 `465` 使用 TLS，其他端口必须支持 STARTTLS，应用拒绝明文 SMTP。邮件标题和正文只接受纯文本模板，支持 `{time}`、`{filename}`、`{format}`，服务端会转义 HTML 并生成受控样式。备份历史（`backup_history` 表）记录目录/邮件两通道的每次执行——成功、失败、跳过都留痕，含触发方式（定时/启动/手动）、范围模式、条数与错误；测试邮件不写入，历史没有删除接口。邮件分片发送时每成功一片即推进备份进度，中途失败的剩余数据会在下次备份时自动续发。Web 管理服务是配置和恢复的唯一交互入口，无法启动时应用会明确退出，不会进入无法再启用的后台状态。

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
├── internal/clipvault/         # 核心逻辑（适配层）
│   ├── domain/                 # 业务模型唯一定义（记录/分组/搜索/清理、事件状态常量）
│   ├── application/            # 运行时状态等应用层
│   ├── storage/duckdb/         # schema v11（正文/事件判别模型 + 稳定事件指纹 + 外部只读令牌/审计表）
│   ├── transfer/               # 导入导出范围契约（export v9）
│   ├── backup/                 # 增量备份策略（纯逻辑）
│   ├── security/               # 密码散列与 DPAPI
│   ├── transport/httpui/       # JSON envelope 与严格解码
│   ├── platform/windows/       # Windows 平台适配
│   ├── bootstrap/              # 命令解析与生命周期装配
│   ├── app_windows.go          # Windows 启动流程 + 恢复入口
│   ├── auth.go                 # 锁屏密码 & 二次密码认证管理
│   ├── autostart_windows.go    # 开机启动（注册表）
│   ├── backup.go               # 定时备份服务（目录备份 + 邮件备份 + SMTP）
│   ├── clipboard_windows.go    # 剪贴板事件读取（文本/文件/格式枚举）
│   ├── clipboard_image_windows.go   # 剪贴板图片捕获（PNG/DIBV5/DIB/BITMAP → PNG）
│   ├── clipboard_restore_windows.go # 恢复记录到剪贴板 + 粘贴到之前窗口
│   ├── config.go               # 配置结构、默认值、环境路径解析
│   ├── config_manager.go       # 配置管理器（内存缓存 + 变更通知）
│   ├── cvexport.go             # 加密导出包（scrypt + AES-256-GCM + gzip）
│   ├── dpapi_windows.go        # Windows DPAPI 加解密
│   ├── filter.go               # 自定义正则过滤引擎
│   ├── keyfile.go              # DPAPI 密钥文件 + 恢复文件读写
│   ├── logger.go               # 按日期分文件日志
│   ├── popup_keys.go           # 弹窗动作快捷键解析与冲突校验
│   ├── recovery.go             # 恢复逻辑（密码恢复 + 新建数据库）
│   ├── recovery_password.go    # 恢复密码管理（设置/修改/安全重置/验证/强度检查）
│   ├── recovery_server.go      # DPAPI 失效时的恢复 HTTP 服务
│   ├── recorder.go             # 可取消采集与正文/事件分流状态机
│   ├── server.go               # HTTP 服务器（API 路由 + 嵌入式前端）
│   ├── server_dataapi.go       # 数据接口管理端点（状态/配置/令牌/审计）
│   ├── dataapi_service.go      # 外部数据 API 独立监听服务（Bearer Token + 治理）
│   ├── dataapi_query.go        # 查询 DSL 白名单校验与 SQL 计划
│   ├── server_mcp.go           # MCP 管理端点
│   ├── mcp_service.go          # MCP Streamable HTTP 服务（/mcp 端点）
│   ├── mcp_tools.go            # MCP 7 个只读工具
│   ├── mcp_resources.go        # MCP 6 个只读资源
│   ├── search_window_windows.go     # 桌面快速搜索弹窗（Win32）
│   ├── shell_windows.go        # Shell 操作（打开目录等）
│   ├── state.go                # 运行时状态（暂停/恢复）
│   ├── statefile.go            # 应用状态文件（引擎、密钥指纹、时间戳）
│   ├── store.go                # DuckDB 存储层（记录 + 配置 + 分组 + 导入导出）
│   ├── store_cleanup.go        # 精确历史清理（preview/delete 共用条件）
│   ├── windows_runtime.go      # Win32 托盘/窗口/消息循环 + 恢复抑制水位
│   ├── windows_clipboard_runtime.go # 只保留最新状态的可取消串行采集邮箱
│   └── web/                    # 前端静态资源（编译时嵌入）
│       ├── app/                # 按历史页/设置页拆分的原生 ES 模块
│       ├── pages/              # 服务端启动时编译的页面片段
│       ├── history.css         # 历史页样式
│       ├── settings.css        # 设置页样式
│       └── tokens.css          # 主题变量与共享基础样式
├── internal/licensing/         # 机器码、Ed25519 验签、DPAPI 授权凭据
├── scripts/build-release.ps1   # Garble 正式发布构建与校验和
├── go.mod
└── go.sum
```

## 技术栈

- **Go 1.26.6+** + CGO
- **DuckDB**（`github.com/duckdb/duckdb-go/v2`）— 加密列式数据库
- **Windows DPAPI** — 用户级密钥保护
- **scrypt + AES-256-GCM** — 恢复密码和加密导出包
- **Ed25519 + DPAPI** — 永久离线激活码验签与当前用户授权凭据保护
- **SMTP** — 定时邮件备份发送
- **Win32 API** — 系统托盘、剪贴板监听与恢复、全局热键、桌面搜索弹窗、SendInput 粘贴
- **embed.FS** — 前端静态资源编译时嵌入
