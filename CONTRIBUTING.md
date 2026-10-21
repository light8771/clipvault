# 参与 ClipVault

首次公开发布仍在准备中。自有代码和文档采用 Apache-2.0，版权署名 `ClipVault contributors`，第三方原声明保留。提交贡献时应有权按此许可提供对应内容；贡献不转让作者版权。不将本地操作授权视为公开上传许可。

## 环境与构建

目标仅 Windows 10/11 x64；安装 Go 1.26.10、MinGW-w64（x86_64/UCRT）、Node.js 22 与 npm、Python 3.12+（发布审查工具用，本轮 3.14.2）。前端无打包步骤。以下 MinGW 路径是示例，按本机安装位置调整：

```powershell
$env:PATH='D:\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED='1'
$env:CC='D:\mingw64\bin\gcc.exe'
go version
gcc --version
npm ci
go test ./... -count=1
go test -race ./... -count=1 -timeout=30m
./scripts/check-web.ps1
./scripts/test-order-build.ps1
git diff --check
```

普通测试与 race 测试通常需要数分钟，必须等待退出码。跳过项不计入通过。激活态端到端测试的合成公钥和命令见 `internal/clipvault/e2e_activated_test.go`，只对临时数据签发凭据。桌面弹窗测试需显式启用，见 [验收说明](scripts/popup-acceptance.md)；不要在真实剪贴板含敏感数据时运行。

优先使用 `go run ./cmd/isolated-preview -lifetime 10m` 预览 Web UI。它不启动生产采集器，不证明原生托盘和跨应用恢复可用。完整应用调试见 README；`dev-run.ps1` 会写授权凭据，必须显式设置独立的 `CLIPVAULT_DATA_DIR`，不要沿用个人数据目录。

## 变更约束

- 先读 [AGENTS.md](AGENTS.md)，保留当前架构、数据库、加密及启动链路。生产文件超过 Go 700 / JS 500 / CSS、HTML 800 行时拆模块，不放宽门禁。
- 不增加旧配置、数据库、路由和导出版本的兼容或迁移逻辑；保留版本不匹配时拒绝打开的行为。
- 新前端模块必须在 `server_static.go` 登记。运行全量验收；针对性测试只能作为补充。
- 缺陷修复要有明确复现条件；用虚构样本覆盖安全边界和数据完整性。界面小修改不需要机械增加测试。
- 提交说明用中文，说明实际问题、改动与验证；一个 PR 聚焦一个问题。不要混入格式重排、历史改写和无关重构。

## Issue 与 PR

缺陷报告提供版本、Windows 版本、构建方式、复现步骤、预期/实际行为及脱敏日志。性能问题提供合成数据规模。PR 写清测试命令、退出码、跳过项和数据影响。不要上传实际数据库、剪贴板、订单清单、机器码、密码、令牌、激活码或签发材料。

安全问题按 [SECURITY.md](SECURITY.md) 私下报告；公开 Issue 不接收漏洞细节和敏感附件。贡献适用 Apache-2.0；署名使用实际贡献记录，不虚构身份。
