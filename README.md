# ClipVault

ClipVault 是一个用 Go 编写的 Windows 剪切板记录工具。后台托盘运行，所有历史写入本地 SQLite，并提供一个内置的 Web 搜索与配置界面。

## 功能

- **托盘后台运行**：右键菜单支持暂停/恢复、搜索历史/设置、打开数据目录、开机自启动、退出
- **全局快捷键**：`Ctrl+Alt+P` 暂停/恢复（可在 Web 设置页配置）
- **星标收藏**：点击卡片上的 ⭐ 按钮收藏条目，支持「收藏」筛选器快速查看已收藏内容
- **自定义过滤**：通过正则表达式屏蔽不想记录的内容（如密码、Token 等），匹配则跳过，其余全部记录
- **文件拖放记录**：复制文件时记录路径清单，并按扩展名自动分类（图片/视频/压缩包/Office/其他文件）
- **哈希去重**：相同内容连续复制只保存一条
- **本地 SQLite 存储**：`%LOCALAPPDATA%\ClipVault\clipvault.db`
- **配置持久化**：配置存储在数据库 `settings` 表中，通过 Web 界面修改，无需手动编辑文件
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
| 一键复制 | 点击「复制」按钮，内容写入系统剪切板并弹出 Toast 提示 |
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
| 热键 | 启用/禁用、修饰键（Ctrl/Alt/Shift/Win）、触发键（字母、数字、F1–F24 等） |
| 过滤 | 自定义正则过滤规则，支持增删，匹配内容不记录 |
| 服务 | Web 监听地址 |
| 导出 | 导出配置（JSON）、导出全部记录（JSON / CSV） |

> 过滤规则和文字限制**即时生效**；热键、监听地址变更需**重启应用**后生效。

> 端口被占时，自动尝试下 5 个端口（如 18765 → 18769），日志记录实际使用的端口。

## 构建

> **第一步：生成 Windows 资源文件**（exe 图标、版本信息、manifest）
>
> ```powershell
> go-winres make --out cmd/clipvault/rsrc
> ```
>
> 初次构建或修改图标后执行一次即可。
>
> **注意**：`--out` 参数是输出文件的**前缀**，不是目录。必须写成 `cmd/clipvault/rsrc`，
> 这样才能在 `cmd/clipvault/` 目录下生成 `rsrc_windows_amd64.syso` 等文件。
> Go 编译器只会链入**与被编译包同目录**的 `.syso` 文件，根目录的 `.syso` 对
> `cmd/clipvault` 包无效。

```powershell
# 开发调试（有控制台窗口，启动后直接打印 Web 访问地址）
go run ./cmd/clipvault

# 控制台版构建（同样打印访问地址）
go build -o bin\clipvault.exe .\cmd\clipvault

# GUI 版（正式发布，无黑窗口）
go build '-ldflags=-H=windowsgui' -o bin\clipvault.exe .\cmd\clipvault
```

> 前两种方式启动后控制台会输出：`Web 界面（搜索+设置）: http://127.0.0.1:18765/`（端口被占时自动顺延）。

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

### Web API

搜索服务同时暴露以下 REST 接口，供外部脚本或自动化使用：

| 接口 | 方法 | 说明 |
|------|------|------|
| `GET /` | GET | 搜索与设置页面 |
| `GET /api/search` | GET | 搜索历史（参数见下表） |
| `GET /api/state` | GET | 返回 `{"paused": false}` |
| `POST /api/pause` | POST | 暂停/恢复（`paused=true/false`，省略则 toggle） |
| `DELETE /api/items?id=42` | DELETE | 删除指定记录 |
| `PATCH /api/items?id=42&starred=1` | PATCH | 收藏（`starred=0` 取消收藏） |
| `POST /api/clear` | POST | 清除全部历史记录 |
| `GET /api/config` | GET | 获取当前完整配置 |
| `PUT /api/config` | PUT | 保存新配置（JSON Body） |
| `GET /api/export/config` | GET | 下载配置文件（JSON） |
| `GET /api/export/data?format=json` | GET | 导出全部记录（`format=json` 或 `format=csv`） |

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

示例：

```powershell
# 搜索最近 20 条包含 "hello" 的记录
curl "http://127.0.0.1:18765/api/search?q=hello&limit=20"

# 只看收藏的图片类记录
curl "http://127.0.0.1:18765/api/search?starred=1&kind=image"

# 暂停记录
curl -X POST "http://127.0.0.1:18765/api/pause?paused=true"

# 删除第 42 条记录
curl -X DELETE "http://127.0.0.1:18765/api/items?id=42"

# 导出全部记录为 CSV
curl "http://127.0.0.1:18765/api/export/data?format=csv" -o clipvault-data.csv
```

## 配置说明

配置存储在数据库 `settings` 表中，通过 Web 设置页面修改（`/` → 设置选项卡），也可直接通过 `PUT /api/config` 接口写入。

默认配置如下：

```json
{
  "maxTextChars": 0,
  "maxFileDropPaths": 0,
  "search": {
    "enabled": true,
    "listenAddr": "127.0.0.1:18765"
  },
  "hotkey": {
    "enabled": true,
    "modifiers": ["ctrl", "alt"],
    "key": "P"
  },
  "sensitive": {
    "customPatterns": []
  },
  "autostart": {
    "enabled": false,
    "appName": "ClipVault"
  }
}
```

| 字段 | 说明 |
|------|------|
| `maxTextChars` | 超过此字符数的文本直接跳过；`0` 表示不限制 |
| `maxFileDropPaths` | 文件拖放时最多记录的路径数；`0` 表示不限制 |
| `search.listenAddr` | Web 服务监听地址；端口被占时自动尝试下 5 个端口 |
| `hotkey.modifiers` | 修饰键列表，可选值：`ctrl`/`alt`/`shift`/`win` |
| `hotkey.key` | 触发键，支持 `A–Z`、`0–9`、`F1–F24`、`SPACE`、`TAB`、`ESC`、`PAUSE` |
| `sensitive.customPatterns` | 自定义正则列表；匹配则不保存，仅在日志记录跳过原因 |
| `autostart.enabled` | 是否开机自启动（也可通过托盘右键菜单切换） |

## 项目结构

```
clipboard_ext/
├── cmd/clipvault/              # 入口：CLI 命令分发
│   ├── main.go
│   ├── icon.png                # 主图标（256×256，替换此文件即可换图标）
│   ├── icon16.png              # 16×16 小图标（可选，用于托盘/小图标视图）
│   ├── rsrc_windows_amd64.syso # Windows 资源（go-winres make 生成，勿手动编辑）
│   └── rsrc_windows_386.syso   # 同上（32 位）
├── internal/clipvault/         # 核心逻辑
│   ├── app_windows.go          # Windows 启动流程
│   ├── autostart_windows.go    # 开机启动（注册表）
│   ├── clipboard_windows.go    # 剪切板监听与读取、文件类型识别
│   ├── config.go               # 配置结构定义与默认值
│   ├── config_manager.go       # 配置管理器（内存缓存 + 变更通知）
│   ├── filter.go               # 自定义正则过滤引擎
│   ├── hotkey.go               # 全局快捷键解析
│   ├── icon_windows.go         # 托盘图标（纯代码绘制）
│   ├── logger.go               # 按日期分文件日志
│   ├── recorder.go             # 剪切板事件处理与持久化
│   ├── search_server.go        # HTTP 服务器（embed.FS，含 API 与静态资源）
│   ├── state.go                # 运行时状态（暂停/恢复）
│   ├── store.go                # SQLite 存储层（记录 + 配置）
│   ├── windows_runtime.go      # Win32 托盘/窗口/消息循环
│   └── web/                    # 前端静态资源（编译时嵌入）
│       ├── index.html
│       ├── style.css
│       └── script.js
├── winres/                     # Windows 资源配置
│   └── winres.json             # go-winres 配置（图标、版本信息、manifest）
├── gen_icon.py                 # 图标生成脚本（修改图标样式后运行）
├── go.mod
└── go.sum
```
