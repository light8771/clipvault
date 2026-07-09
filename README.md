# ClipVault

ClipVault 是一个用 Go 编写的 Windows 剪贴板管理工具。后台托盘运行，所有历史写入**加密的 DuckDB 数据库**，并提供内置 Web 搜索与配置界面。数据库密钥通过 Windows DPAPI 保护，支持恢复密码备份与跨设备恢复。

## 功能

- **托盘后台运行**：右键菜单支持暂停/恢复、搜索历史/设置、打开数据目录、开机自启动、退出
- **全局快捷键**：`Ctrl+Alt+P` 暂停/恢复（可在 Web 设置页配置）
- **星标收藏**：点击卡片上的 ⭐ 按钮收藏条目，支持「收藏」筛选器快速查看
- **自定义过滤**：通过正则表达式屏蔽不想记录的内容（如密码、Token 等）
- **文件拖放记录**：复制文件时记录路径清单，按扩展名自动分类（图片/视频/压缩包/Office/其他）
- **哈希去重**：相同内容连续复制只保存一条
- **DuckDB 加密存储**：数据库使用 AES-256 加密，密钥由 DPAPI 保护
- **恢复密码**：可设置恢复密码备份数据库密钥，DPAPI 失效时（换电脑、重装系统）可恢复数据
- **锁屏密码 & 二次密码**：Web 界面访问密码 + 敏感操作二次验证
- **加密导出包**：支持 `.cvexport` 加密格式，用密码加密导出数据，安全传输或备份
- **分组管理**：自定义分组对剪贴板记录分类，支持嵌套
- **便携模式**：exe 同目录放置 `portable.flag` 文件即可切换为便携模式，数据存放在 exe 目录下
- **日志按日期分文件**：exe 同目录下的 `logs\YYYY-MM-DD.log`

### Web 界面（搜索 + 设置）

双击托盘图标或右键「搜索历史 / 设置」打开内置 Web 页（默认 `http://127.0.0.1:18765/`）：

#### 历史视图

| 功能 | 说明 |
|------|------|
| 实时搜索 | 输入即搜，180ms 防抖 |
| 关键词高亮 | 命中文本用橙色 `<mark>` 标注 |
| 类型过滤 | 按 `文本` / `图片` / `视频` / `压缩包` / `Office` / `其他文件` 分类筛选 |
| 收藏筛选 | 点击「收藏」chip，只显示已星标条目 |
| 日期范围筛选 | 选择起止日期过滤历史记录 |
| 分页 | 每页 50/100/200 条可选，显示当前页范围与总数 |
| 相对时间 | 显示「3 分钟前」，悬浮可看完整时间 |
| 一键复制 | 点击「复制」按钮，内容写入系统剪贴板并弹出 Toast 提示 |
| 弹窗查看 | 长文本点击「查看」按钮弹出全文预览，支持弹窗内复制，`Esc` 关闭 |
| 删除单条 | 点击「删除」按钮，卡片滑出动画后移除 |
| 清除全部 | 顶栏「清除」按钮，需二次确认后删除所有记录 |
| 暂停/恢复 | 顶栏按钮一键切换录制状态 |
| 自动刷新 | 勾选后每 3 秒拉取最新记录 |
| 键盘快捷键 | `Ctrl+K` 聚焦搜索框，`Esc` 清空并失焦 / 关闭弹窗 |
| 深浅色主题 | 自动跟随系统 `prefers-color-scheme` |

#### 设置视图

| 分区 | 可配置项 |
|------|---------|
| 基本 | 最大文字长度（0 = 不限）、最大文件路径数（0 = 不限） |
| 服务 | Web 监听地址 |
| 安全 | 锁屏密码、二次密码、数据恢复密码（设置/修改/删除）、锁屏会话超时、二次认证缓存时长 |
| 过滤 | 自定义正则过滤规则，支持增删，匹配内容不记录 |
| 分组 | 创建/编辑/删除分组，支持嵌套，可将记录加入分组 |
| 导出 | 导出配置（JSON）、导出全部记录（JSON / CSV / 加密 .cvexport）、导入数据（JSON / .cvexport） |

> 过滤规则和文字限制**即时生效**；监听地址变更需**重启应用**后生效。

> 端口被占时，自动尝试下 5 个端口（如 18765 → 18769），日志记录实际使用的端口。

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
- **clipvault.recovery**：db_key 经用户恢复密码（scrypt + AES-256-GCM）加密后存储
- **clipvault.state**：元数据文件，记录数据库引擎、密钥指纹、是否已设置恢复密码

### DPAPI 失效恢复

当 DPAPI 失效（换电脑、重装系统、用户配置损坏）时：

1. 应用启动检测到 `OpenStore` 失败，自动启动恢复 HTTP 服务
2. 浏览器打开恢复页面，用户输入恢复密码
3. 用恢复密码解密 `clipvault.recovery` 获取 db_key
4. 验证 db_key 能打开 DuckDB 数据库
5. 用新用户的 DPAPI 重新加密 db_key，写入 `clipvault.key`
6. 应用正常启动

## 构建

> **前提**：需要 MinGW（CGO 依赖，DuckDB 需要 C 编译器）和 Go 1.25+

```powershell
# 设置 CGO 和 MinGW 路径
$env:CGO_ENABLED = 1
$env:PATH = "D:\mingw64\bin;" + $env:PATH

# 开发调试（有控制台窗口）
go run ./cmd/clipvault

# 控制台版构建
go build -o bin\clipvault.exe .\cmd\clipvault

# GUI 版（正式发布，无黑窗口）
go build '-ldflags=-H=windowsgui' -o bin\clipvault.exe .\cmd\clipvault
```

> Windows 资源文件（`.syso`）已预生成在 `cmd/clipvault/` 目录下，包含图标和版本信息。修改图标后需用 `rsrc` 工具重新生成。

## 使用

```powershell
.\bin\clipvault.exe                     # 托盘后台运行
.\bin\clipvault.exe run                 # 同上
.\bin\clipvault.exe search 关键字       # 命令行搜索历史
.\bin\clipvault.exe autostart on        # 开启开机启动
.\bin\clipvault.exe autostart off       # 关闭开机启动
.\bin\clipvault.exe autostart status    # 查看开机启动状态
.\bin\clipvault.exe config              # 打印配置文件路径
.\bin\clipvault.exe data                # 打印数据目录路径
```

### 便携模式

在 exe 同目录下创建 `portable.flag` 空文件即可启用便携模式：

```powershell
New-Item -ItemType File "portable.flag"
.\bin\clipvault.exe
```

便携模式下数据存放在 `exe目录\data\`，日志在 `exe目录\logs\`。

### 数据目录优先级

1. `--data-dir` 命令行参数
2. `CLIPVAULT_DATA_DIR` 环境变量
3. `portable.flag` 文件存在 → `exe目录\data\`
4. `%LOCALAPPDATA%\ClipVault\`（默认）
5. `%APPDATA%\ClipVault\`（降级）

## Web API

| 接口 | 方法 | 认证 | 说明 |
|------|------|------|------|
| `GET /` | GET | — | 搜索与设置页面 |
| `POST /api/auth/login` | POST | — | 锁屏密码登录 |
| `POST /api/auth/secondary` | POST | — | 二次密码验证 |
| `GET /api/search` | GET | 锁屏 | 搜索历史 |
| `GET /api/state` | GET | 锁屏 | 返回运行状态 |
| `POST /api/pause` | POST | 锁屏 | 暂停/恢复 |
| `DELETE /api/items?id=N` | DELETE | 锁屏 | 删除指定记录 |
| `PATCH /api/items?id=N&starred=1` | PATCH | 锁屏 | 收藏/取消收藏 |
| `POST /api/clear` | POST | 锁屏 | 清除全部记录 |
| `GET /api/config` | GET | 锁屏 | 获取配置 |
| `PUT /api/config` | PUT | 二次 | 保存配置 |
| `GET /api/export/config` | GET | 二次 | 导出配置（JSON） |
| `GET /api/export/data?format=json` | GET | 二次 | 导出记录（json/csv/cvexport） |
| `POST /api/import/data` | POST | 二次 | 导入数据（JSON 或 .cvexport） |
| `GET /api/recovery/status` | GET | 二次 | 查询恢复密码状态 |
| `POST /api/recovery/set` | POST | 二次 | 设置恢复密码 |
| `POST /api/recovery/change` | POST | 二次 | 修改恢复密码 |
| `POST /api/recovery/remove` | POST | 二次 | 删除恢复密码 |
| `GET /api/groups` | GET | 锁屏 | 列出分组 |
| `POST /api/groups` | POST | 锁屏 | 创建分组 |
| `PUT /api/groups` | PUT | 锁屏 | 更新分组 |
| `DELETE /api/groups` | DELETE | 锁屏 | 删除分组 |

**`/api/search` 查询参数：**

| 参数 | 说明 |
|------|------|
| `q` | 搜索关键词 |
| `limit` | 每页条数（默认 50，上限 500） |
| `offset` | 分页偏移 |
| `starred=1` | 只返回已收藏条目 |
| `kind` | 按类型筛选：`text` / `image` / `video` / `archive` / `office` / `files` |
| `date_from` | 起始日期（`YYYY-MM-DD`，含） |
| `date_to` | 结束日期（`YYYY-MM-DD`，含） |

**加密导出示例：**

```powershell
# 加密导出（需要密码）
curl "http://127.0.0.1:18765/api/export/data?format=cvexport&password=mypassword" -o clipvault-data.cvexport

# 导入加密文件（需要密码）
curl -X POST "http://127.0.0.1:18765/api/import/data?password=mypassword" --data-binary @clipvault-data.cvexport
```

## 配置说明

配置存储在 DuckDB 数据库的 `settings` 表中，通过 Web 设置页面修改，也可通过 `PUT /api/config` 接口写入。

```json
{
  "maxTextChars": 0,
  "maxFileDropPaths": 0,
  "search": {
    "enabled": true,
    "listenAddr": "127.0.0.1:18765"
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
  }
}
```

| 字段 | 说明 |
|------|------|
| `maxTextChars` | 超过此字符数的文本直接跳过；`0` 表示不限制 |
| `maxFileDropPaths` | 文件拖放时最多记录的路径数；`0` 表示不限制 |
| `search.listenAddr` | Web 服务监听地址；端口被占时自动尝试下 5 个端口 |
| `sensitive.customPatterns` | 自定义正则列表；匹配则不保存 |
| `autostart.enabled` | 是否开机自启动 |
| `security.lockSessionTimeout` | 锁屏会话超时（分钟），0 = 关闭浏览器后失效 |
| `security.secondaryAuthTimeout` | 二次认证缓存时长（分钟），0 = 每次都校验 |

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
├── internal/clipvault/         # 核心逻辑
│   ├── app_windows.go          # Windows 启动流程 + 恢复入口
│   ├── auth.go                 # 锁屏密码 & 二次密码认证管理
│   ├── autostart_windows.go    # 开机启动（注册表）
│   ├── clipboard_windows.go    # 剪贴板监听与读取、文件类型识别
│   ├── config.go               # 配置结构、默认值、环境路径解析
│   ├── config_manager.go       # 配置管理器（内存缓存 + 变更通知）
│   ├── cvexport.go             # 加密导出包（scrypt + AES-256-GCM + gzip）
│   ├── dpapi_windows.go        # Windows DPAPI 加解密
│   ├── filter.go               # 自定义正则过滤引擎
│   ├── keyfile.go              # DPAPI 密钥文件 + 恢复文件读写
│   ├── logger.go               # 按日期分文件日志
│   ├── recovery.go             # 恢复逻辑（密码恢复 + 新建数据库）
│   ├── recovery_password.go    # 恢复密码管理（设置/修改/删除/验证/强度检查）
│   ├── recovery_server.go      # DPAPI 失效时的恢复 HTTP 服务
│   ├── recorder.go             # 剪贴板事件处理与持久化
│   ├── search_server.go        # HTTP 服务器（API + 嵌入式前端）
│   ├── shell_windows.go        # Shell 操作（打开目录等）
│   ├── state.go                # 运行时状态（暂停/恢复）
│   ├── statefile.go            # 应用状态文件（引擎、密钥指纹、恢复标记）
│   ├── store.go                # DuckDB 存储层（记录 + 配置 + 分组 + 导入导出）
│   ├── windows_runtime.go      # Win32 托盘/窗口/消息循环
│   └── web/                    # 前端静态资源（编译时嵌入）
│       ├── index.html
│       ├── style.css
│       └── script.js
├── go.mod
└── go.sum
```

## 技术栈

- **Go 1.25** + CGO
- **DuckDB**（`github.com/duckdb/duckdb-go/v2`）— 加密列式数据库
- **Windows DPAPI** — 用户级密钥保护
- **scrypt + AES-256-GCM** — 恢复密码和加密导出包
- **Win32 API** — 系统托盘、剪贴板监听、全局热键
- **embed.FS** — 前端静态资源编译时嵌入
