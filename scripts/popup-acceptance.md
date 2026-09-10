# Windows 快捷搜索验收

2026-09-10，Windows/amd64，Go 1.26.6 + MinGW CGO。

## 实现约束

- 粘贴由弹窗消息线程上的计时器驱动：等待快捷键释放 → 恢复剪贴板并请求目标窗口激活 → 确认前台窗口 → 关闭弹窗并发送 Ctrl+V。
- 等待期间保持消息循环响应；Esc、关闭按钮和用户切换窗口均可取消。释放等待上限 3 秒，激活等待上限 500 毫秒。切换阶段再次按下修饰键/Enter、目标销毁或目标改变均停止注入。
- 跨输入队列的 WM_ACTIVATE 允许缺少另一窗口句柄，最终以 GetForegroundWindow 确认目标；关闭弹窗后再次检查。
- INPUT 的 x64 大小为 40 字节，字段偏移有回归测试。部分注入失败只释放本次注入的按键，不重试粘贴。
- 快捷键与搜索框方向键统一在 TranslateMessage 前处理；Tab/Shift+Tab 使用原生对话框导航。没有输入框子类化、每次打开新建回调、AttachThreadInput 或阻塞等待循环。
- 自动粘贴失败时提供本地化提示并记录错误。已恢复剪贴板和未执行恢复采用不同提示。

## 已完成的真实桌面验证

使用生产搜索弹窗、临时 DuckDB 中的两条合成记录，以及独立空白记事本。通过 Computer Use 发送按键，先观察焦点，再从记事本全选复制实际结果进行逐字比较。

| 场景 | 验收结果 |
| --- | --- |
| 搜索框焦点，Ctrl+Enter | 回到原记事本编辑区，内容严格等于“验收第一条：CTRL-PASTE-123” |
| Shift+Tab 进入列表，Down 选第二条，Ctrl+Enter | 列表确实获得焦点；原记事本选区被“验收第二条：ALT-PASTE-456”准确替换 |
| 配置为 Alt+Enter | 内容严格等于“验收第一条：CTRL-PASTE-123”；没有额外换行 |
| Down 选第二条，Shift+Enter | 剪贴板更新为第二条，记事本仍严格保留第一条，没有自动粘贴 |
| Esc | 弹窗关闭，返回记事本，未额外粘贴 |

自动化工具不枚举 WS_EX_TOOLWINDOW，因此**仅在验收夹具中**移除该扩展样式并加入 WS_EX_APPWINDOW。生产弹窗样式未改变。截图接口在此机器不可用，验证使用可访问性焦点信息及实际文档文本。鼠标双击、不同权限级别应用、非文本内容未做本轮桌面验证。

## 自动化回归

- 正式源码包的全量测试通过（`internal/clipvault` 391.558 秒，其余 `internal` 和 `cmd` 包通过）。
- 最终改动再次运行全仓库定向回归，包含 INPUT ABI、松键/切换/超时/窗口销毁/修饰键状态、跨线程空句柄激活、关闭取消、快捷键、剪贴板恢复、i18n；通过，`internal/clipvault` 25.663 秒。
- 首次 `go test ./...` 被已忽略的临时文件 `output/playwright/media-harness.go` 干扰；保留内容并将其改名为 `_media-harness.go`，让 Go 正确忽略这个不能独立编译的夹具。

## 复现

```powershell
$env:PATH='D:\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED='1'
$env:CC='D:\mingw64\bin\gcc.exe'
& 'C:\Program Files\Go\bin\go.exe' test ./... -run 'Test(Paste|Popup|ParsePopup|FormatSuggestion|.*ClipboardRestore|.*I18n.*)' -count=1

# 手动桌面验收；仅明确设置此环境变量时才会注册热键和显示窗口。
$env:CLIPVAULT_POPUP_ACCEPTANCE='1'
& 'C:\Program Files\Go\bin\go.exe' test ./internal/clipvault -run '^TestSearchPopupDesktopAcceptance$' -count=1 -v -timeout=15m
Remove-Item Env:CLIPVAULT_POPUP_ACCEPTANCE
```

夹具热键：Ctrl+Alt+F12 从当前窗口唤起弹窗；Ctrl+Alt+F10 切换 Ctrl+Enter/Alt+Enter 配置；关闭弹窗后 Ctrl+Alt+F11 结束夹具。夹具不启动录制器，不读取生产数据库。验收产生的记事本文本保留供核对，不替换运行中的正式 EXE。
