# ClipVault 首次开源准备执行报告

日期：2026-10-21（Asia/Shanghai）。**源码已公开：https://github.com/light8771/clipvault，默认分支 main**。已按维护者后续授权上传清理后的发布分支；原 codex 分支保留在保存提交 2efd52d，本地原件未删除，50 个内部路径的历史版本未进入公开分支。公开克隆及构建已验证，GitHub CI 状态见下方第 11 节。EXE/Release 和 OpenAI 申请均未发布或提交；正式二进制发行仍需独立验收。

## 2026-10-21 后续决定（当前方案）

- 维护者授权选择 **Apache-2.0**，署名 **Copyright 2026 ClipVault contributors**，已写 LICENSE/NOTICE 及 Windows 资源。各贡献者保留各自权利，第三方声明不重新许可。
- 公开全部有权许可的自有源码和文档，包含离线授权/签发工具并保留现有商业激活实现；允许依 Apache-2.0 修改、构建及商业再分发，官方收费服务不能缩减这些权利。
- **不采用独立无历史仓库**。原 codex 分支在 2efd52d 保存全部准备改动；已按用户授权另建 codex/oss-release-preparation 并清理内部材料历史。126 个原提交的演进关系保留，排除 50 个历史路径，所有 126 个提交哈希变化；逐提交保留文件的字节与模式一致。原分支及其他引用、私人原件留在本地，不随发布分支上传。
- 安全联系方式和应用支持元数据统一为 **foobar**。占位文本不计为已验证可达渠道；不填造申请账号字段。
- 已执行本地保存提交及仅发布分支的授权历史清理；未改名/创建本地 main、未推送。后续文档更正仅在发布分支新增普通提交。下文最初盘点与测试数据为对应阶段记录，不当作本轮重跑结果。

## 1. 基线及授权边界

- 当前项目 `clipboard_ext`，分支 `codex/clipvault-modular-refactor`；HEAD `f61c7c8`（2026-10-13），所有本地引用可达提交 128 个，包含开发分支、`ugit-stash/*` 和 stash。
- 开始时已有 `AGENTS.md` 修改，任务书及 `docs/ai-handoff/` 未跟踪。保留其内容；仅在 AGENTS 既有规则上更新安全补丁基线、增加公开约束。
- 有 1 个 origin，指向本地路径类型远端。未更改/连接该远端；未收集私人账号，Git 作者不等同于申请人或版权人。
- 未创建分支/提交/Tag，未清空数据库，未运行 dev-run/清库脚本，未替换正式 EXE，未删除原签发材料。默认用户数据目录仅检查是否存在，没有打开用户数据库或读取实际剪贴板正文用于报告。
- 没有创建 GitHub 仓库、push、上传源码/二进制、发布 Release、填写或提交申请。未来命令仅写入发布计划。

## 2. 项目盘点与构建入口

| 范围 | 已核实事实 |
| --- | --- |
| 平台/入口 | Windows 10/11 x64；`cmd/clipvault` → `bootstrap` → Windows 托盘、剪贴板、热键、原生搜索 |
| 分层 | `domain`、`application`；`storage/duckdb`、`security`、`transfer`、`backup`；HTTP/Windows 适配；架构与规模门禁由测试约束 |
| 存储 | 加密 DuckDB，随机数据库密钥，DPAPI 密钥保护及恢复密码封装；schema v13 |
| 内容 | 文本、HTML/RTF、图片、文件路径、复合内容及独立事件；原文保存、安全预览和恢复分层 |
| 传输 | JSON/加密包内容 v10、XLSX v9；`.cvexport` 容器 v2、魔数 `CVEXP003`；旧版拒绝，无迁移 |
| 前端 | 原生 ES Modules、嵌入 HTML/CSS/JS，无前端打包及运行时 npm 依赖；开发检查 Acorn 8.18.0 |
| 外部服务 | 独立 HTTP Data API 与 MCP Streamable HTTP，共享 `ExternalReadService` 的敏感可见性策略 |
| 备份 | 目录/SMTP 定时备份、范围/增量、加密包及分片；不等于自动历史保留期限策略 |
| 授权 | Ed25519 离线激活、DPAPI 凭据；空公钥时 unconfigured，录制/备份禁用；未改变商业机制 |
| 发行/功能边界 | 单 EXE、portable.flag 便携模式；没有已验收安装器。OCR、单条语义标签仍是 TODO，未打包 OCR 模型 |

Module `clipboard_ext`，DuckDB Go v2.10504.0 / bindings v0.10504.0。原 Go 1.26.6，本轮安全补丁后 **Go 1.26.10**；已安装官方独立 SDK。已实际检查 MinGW-w64 x64 UCRT **GCC 16.1.0**（`D:\mingw64\bin\gcc.exe` 存在）、Node **22.21.1**、Python **3.14.2**。

运行时版本 0.1.0，原 Windows 资源配置 1.0.0.0；本轮统一为 **0.1.0.0**，由已有 `.rc` 使用 MinGW windres 重新生成 amd64 资源，图标源文件未改。没有 Git Tag，未核实公开版本记录。CHANGELOG 仅使用实际提交日期，不伪造发版日期。

实际构建入口仍为 `scripts/build-release.ps1`；默认普通构建，`-Obfuscate` 才使用 Garble。没有发明替代发布入口。所有 DuckDB Go 测试/构建使用：

```powershell
$env:PATH='D:\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED='1'
$env:CC='D:\mingw64\bin\gcc.exe'
```

## 3. 安全审计与私人数据

### 范围和证据

`python -X utf8 scripts/audit-oss.py` 逐 blob 扫描所有引用可达历史，另查索引和工作区（含忽略文件）。最终对象扫描记录 **128 提交、2105 可达历史 blob、881 不可达 blob、489 索引文件、1258 工作区文件，读取错误 0**；包含 reflog 的提交计数同为 128。不可达对象也检查凭据模式，不会随新源码快照发布。本轮输出目录排除以避免读取正在写入的日志；`.git` 由对象扫描覆盖，node_modules 通过锁文件/许可/安装检查。后续文档补写不改变历史，ZIP 另由打包器逐个检查实际纳入文件。

规则包括私钥标记、GitHub/API/cv_ Token、激活码、凭据赋值、邮箱、敏感扩展名及媒体清单。仅输出路径/类别/行号，不输出匹配值。启发式扫描不能证明没有未知秘密、截图文字或单位代码。不可把无命中写成绝对安全保证。

本地证据位于忽略目录 `.audit/oss-20261021/`，包括 scan.json、scan-final.json、scan-all-objects.json、测试及构建日志；**不是公开附件，不应整体上传**。881 个不可达 blob 没有高识别度凭据命中；普通赋值和邮箱仍有模式候选，来源无法全部定位，不承诺将这些对象公开。

隔离预览以 30 分钟 lifetime 正常退出（退出码 0），已验证其临时数据目录不存在；只关闭本轮专用 Chromium 会话，没有操作其他用户窗口。

| 分类 | 发现 | 处理及影响 |
| --- | --- | --- |
| 已修复的发布内容缺陷 | 原历史含日志、内部审计图文、AI 工具目录、开发计划与临时辅助材料 | 已从发布分支全部 126 个原提交排除共 50 个路径；逐提交保留文件比对一致。原分支及本地原件保留，不可上传原分支或全部引用；详见历史清理记录 |
| 条件风险 | 本地有忽略的开发/订单私钥及口令、数据库、备份、调试输出；默认用户数据目录存在 | 未解密/删除/纳入快照；不打包工作目录。若真实凭据曾外发应先撤销/轮换，删文件或改历史不能撤销泄露 |
| 条件风险 | 历史/索引没有高识别度 Token/私钥/激活码命中；忽略文件中有旧 EXE 备份私钥标记字符串及测试 MCP Token 候选 | 标记字符串不证明私钥泄露，测试 Token 当前用途不确定。仅记录位置并排除发布；如复用到实际实例，由所有者撤销 |
| 已修复缺陷 | application_meta 中虚构支持邮箱、QQ | 上一轮清空虚构值；本轮按维护者要求全部设为 foobar，占位不当作已验证真实渠道 |
| 已修复安全依赖 | Go 1.26.6、Excelize 2.11.0 命中标准库及 XLSX 解析安全公告 | Go 更新 1.26.10；Excelize 固定 `v2.11.1-0.20261020033641-611b222fd8f7`，连带 MVS 必需依赖；没有更换数据库或格式 |
| 加固 | 根忽略规则未覆盖全部签名/数据类型 | 补 env、数据库/备份、证书、密钥/凭据、日志、私人交接、缓存；明确不影响已跟踪文件和历史 |

历史普通凭据赋值候选已识别为测试虚构值、配置脱敏逻辑、旧前端字段，不据此声称真实泄露。版权和未识别个人信息仍需所有者确认。

Excelize 上游公告示例：[行号导致无界迭代](https://github.com/qax-os/excelize/security/advisories/GHSA-jw42-f3rr-4cc3)、[CFB 解析分配导致崩溃/内存耗尽](https://github.com/qax-os/excelize/security/advisories/GHSA-x2q3-8cjh-766f)。公告尚无正式 patched release 标签；本轮固定包含修复的上游提交并重新扫描/回归，不把预发布提交说成正式补丁发行。

### Data API、MCP 与隐私边界

- UI 默认 `127.0.0.1:15678`；API/MCP 默认关闭，默认 `127.0.0.1:17839` / `127.0.0.1:17840`。可配置非回环及全接口监听；UI 非回环要求锁屏密码。**HTTP 不加密**，密码/Token 不能防窃听。保留现有 LAN 功能，未引入自制加密协议。
- 外部仅 Authorization Bearer 认证，HTTP/MCP 通道隔离；256-bit 随机 Token、哈希存储、到期/撤销；默认不给敏感权限。共享读取层限制列表、单条、批量、内容、计数/统计等。再生成保留原权限/有效期并撤销旧 Token，README 已纠正。
- 主 UI 有 Host/Origin、CSRF、CSP 与会话门禁；Data API 不开放通配 CORS，MCP 经 SDK 处理并受 Bearer 认证限制。没有发现默认匿名敏感正文读取路径；这是代码审阅及自动化结论，不等于完整渗透测试。
- 已有速率、并发、分页、请求/响应大小和超时限制。审计保存受限查询摘要/请求体，**搜索词可能含秘密**；不记录 Authorization 原文不代表日志无敏感数据。
- 密钥封装/加密包使用 scrypt + AES-256-GCM，未改协议。数据库加密/DPAPI 不覆盖图片、恢复缓存、媒体、日志和明文导出；PRIVACY/SECURITY 已披露。
- 富文本可读取本地图片或请求 HTTP(S) 图片；不带浏览器 Cookie，限制跨源私网及重定向，同源私网仍可访问。SMTP 强制 TLS/STARTTLS；网络盘/邮件服务形成副本，不能宣称零联网。
- 删除/清理先提交记录，再移除无引用托管图片或恢复缓存；共享图片保留到最后引用删除。canonical 路径校验限制图片在受管目录内。相关资产回归包含在全量测试中；清理失败仍可能遗留文件，不是安全擦除。
- 数据目录搬移复制后切换/清理源目录，目标实际可用性和故障条件仍需专项实机验收；本轮未动真实目录或重写核心链路。此为条件风险，不能引用旧交接结论替代验证。

## 4. 版权、依赖与许可证

**现已采用 Apache-2.0，根目录 LICENSE 为官方完整原文，NOTICE 署名 Copyright 2026 ClipVault contributors**。由维护者授权选择，范围详见 `docs/LICENSE_REVIEW.md`；不是等待再次选择许可证。集体署名不转让实际权利，受雇/委托/第三方代码或素材的来源仍需尊重真实权属。Git 作者和 gen_icon.py 不能单独证明权利来源。

商业激活完整保留，全部有权许可的自有源码和文档按 Apache-2.0 开放，包括授权检查和签发工具。接受者可依许可修改授权检查、构建及商业再分发；官方收费二进制/支持服务不能另行限制这些源码权利。没有拆出专有模块或修改录制/备份门禁。

许可收集覆盖 **107 个来源**（105 Go 模块、Go 工具链、Acorn）、**149 份原始许可/声明**，逐文件 SHA-256 在 `docs/third-party/inventory.json`。含测试、工具和非 Windows 模块，不代表全部链接进 EXE。保留 MIT/BSD/Apache/MPL-2.0 等原文，不给第三方重新授权。

未关闭：Substrait protobuf Go v0.78.1 模块归档及对应上游提交根目录缺许可原文；DuckDB 原生静态库内嵌组件声明、MinGW/UCRT 分发边界、图标/产品名归属仍需核实。**Go wrapper 的 MIT 不概括原生组件全部许可**。未发现仓库字体包、商业 OCR SDK 或分发模型，不等于全部权利已确认。

**按交付物修正范围**：当前依赖通过 go.mod/go.sum 声明并由 Go modules 下载；当前 Git 索引未发现 vendor、node_modules 或第三方 EXE/DLL/静态库文件。仅上传自有源码和依赖声明，不等于把整个依赖仓库或原生库二进制分发出去，不要求为了源码上传打包全部模块许可证。Substrait 下载包缺许可原文是待核实线索，不单凭这一点判定源码不能公开。实际复制进仓库的第三方源码、素材、声明，以及历史中的实际纳入内容，仍按对应内容核查；不能据当前索引推断全部历史。提供 EXE/安装包时，编译链接进去的组件属于分发材料核查范围，不能通过让接收者自行遵守来代替发行方适用的声明义务。Go modules 的下载、版本锁定与校验不会自动处理这些声明。

## 5. 完成的修改

- 完善中文 README，新增英文版，补安装/便携、数据位置、隐私、测试、FAQ、许可/贡献入口；修正旧 schema/export 和再生成 Token 文案。
- 新增 CONTRIBUTING、SECURITY、PRIVACY、CHANGELOG、THIRD_PARTY_NOTICES、权利审查和上游许可清单；安全渠道按后续指示以 foobar 占位，没有编造可达性或 SLA。
- 新增 Issue/PR 模板及无私钥 Windows CI，无 Release 上传；**远端 CI 未执行，不计通过**。
- 新增脱敏只读扫描、许可收集、申请字符校验、白名单快照工具；排除私人交接、内部审计、旧输出/备份/订单/历史。
- 新增 GitHub 发布计划和英文申请草稿；不猜测账号或组织信息。
- 两张截图均为隔离空库（1440px），未用实际剪贴板或旧内部截图。AGENTS 保留原门禁并增加隔离/敏感数据约束。
- 最小源码/脚本修复：清空虚构支持信息、清除订单回归预期失败遗留退出码 7、更新安全依赖、统一 Windows 资源版本。没有核心架构、授权机制、数据库或兼容规则重构。

## 6. 验证证据

下表完整 Go/竞态与浏览器结果为**上一轮行为代码基线**。本次仅文档、许可证、元信息常量及 Windows 版权资源更新，不将旧全量记录冒充本轮重跑。追加定向维护页/规模门禁/静态模块登记测试 3 项通过、退出 0；另运行原发布脚本构建 V2，退出 0。证据为 followup-decisions-test.jsonl、followup-decisions-build.log。

| 命令/检查 | 实际结果 |
| --- | --- |
| 原 `go test ./... -count=1 -json` | 退出 0；11 测试包，1131 测试/子测试通过、5 跳过；主包 350.561s（go-test.jsonl） |
| 修复后同命令 | 退出 0；11 测试包，1131 通过、5 跳过；主包 364.154s（go-test-final.jsonl） |
| 原及修复后 `go test -race ./... -count=1 -json` | 两次退出 1，主包 602.224s / 602.209s，默认 10 分钟超时；不计通过，不以无 DATA RACE 提示冒充成功 |
| `go test -race ./... -count=1 -timeout=30m -json` | **退出 0；11 测试包，1131 测试/子测试通过、5 跳过，DATA RACE 警告 0，主包 1519.290s**。大备份 JSON/CVExport 子用例分别 412.79s / 390.88s，故不能沿用旧交接的约 464s 估计（go-race-30m.jsonl） |
| 激活态 API/MCP E2E | 按测试头注入合成公钥，仅跑 `^TestActivatedExternalServiceE2E$`：退出 0，1 项通过，3.322s（activated-e2e.jsonl） |
| `npm ci`、`./scripts/check-web.ps1` | 退出 0；最终 155 通过、0 失败、0 跳过，4386.7011ms（web-final.log） |
| `./scripts/test-order-build.ps1` | 首次断言成功却遗留退出 7；修复后退出 0，固定/随机 seed 及失败传播三个场景；mock 未生成生产私钥（order-build-final.log） |
| `gofmt -l`、`git diff --check` | 无 Go 格式输出，退出 0；Git 检查退出 0，只有 CRLF 提示；不假称 diff 覆盖未跟踪文件 |
| `go mod verify`、package list、Python 语法 | 最终退出 0；依赖校验成功；16 项目包、11 个有测试。单独 package list 首次漏设 CGO 失败，按已知 MinGW 环境重跑 0，未误判缺编译器 |
| 原 `govulncheck -json ./...` | 退出 0 仍有 21 公告/700 finding（含调用链与模块级）；不作为安全通过（govulncheck.jsonl） |
| 最终 `govulncheck -format text ./...` | 退出 0；调用路径 0 漏洞、已导入包 0；另 6 个仅模块级公告、代码未调用对应功能；不覆盖原生库全部代码（govulncheck-final.log） |
| Chromium 18 页 | 全部 HTTP 200；错误/失败请求数组均 0，控制台 Errors 0 / Warnings 0（browser-routes-final.log、browser-console-final.log） |
| 历史/设置响应式 | 1440/1024/768/390px 共 8 组合，横向溢出 0；另加载暗色+减少动画；没有完整对比度/键盘无障碍认证（browser-responsive.log） |
| 路由 | history、settings/navigation、index.html、script.js、api/session 均 404；api/v1/session 200（browser-contracts.log） |
| 申请字段 | ASCII 字符 420 / 383 / 462，各 ≤500、与文档一致；check-oss-application.py |
| build-release.ps1 普通构建 | 最终退出 0，开发公钥注入，无私钥/模型/用户目录打包（build-final.log） |
| 白名单独立重建 | 无 .git 的快照独立目录，用原脚本退出 0；与工作区 EXE SHA-256 完全相同。首次哈希比较相对路径错误，改准确路径再次核实 True |

自有项目文档本地链接检查无缺失；新增自有文本尾随空白检查无命中，第三方许可原文不作格式改写。根忽略规则对 env、数据库、备份、签名/凭据、私人配置、内部审计等 16 个代表路径均生效；原跟踪与历史内容仍保留。Python 工具要求 3.12+，已在 3.14.2 校验。

5 个跳过为激活态 E2E（已另补跑通过）、桌面弹窗 1 项、需符号链接权限 3 项。后 4 项仍未验收，不计通过；没有提权或改系统策略隐藏跳过。

**未执行**：干净 Windows 10/11 虚拟机，微信/QQ/Word/WPS/Excel/资源管理器真实复制恢复，完整托盘/退出重启/升级卸载，真实 SMTP 服务商，搬移故障演练，百万级历史容量，真实 Garble 混淆和 Authenticode 签名。Web 预览不替代原生功能；没有安装器就不能报告安装器验收通过。

## 7. 候选产物

**元信息与 EXE 文件审查候选为 V2**：`dist/oss-review-decisions-v2/`，包含 Apache-2.0/NOTICE、foobar 支持信息；SHA-256 清单在该目录。后续历史清理和文档更正已超出该 ZIP 的文档快照，源码发布以当前清理后的 Git 分支为准。文件审查 ZIP 不含 .git，仅用于审阅，不是无历史公开仓库方案。原 `dist/oss-review/` 保留为上一轮证据，不能当作当前版本上传。

以下 EXE 哈希及独立快照重建记录均属于上一轮 V1，不冒充 V2：

`dist/oss-review/ClipVault.exe`：**69,097,472 bytes**，Windows amd64，Go 1.26.10，版本 0.1.0.0，**NotSigned**，注入已有开发公钥，**只作本地审查**。SHA-256：

```text
8C1FDD94373C187A0AD030F90A2108C61AEEFA7464EC8EE4766157B602BDE077
```

PE 导入表仅见 Windows/UCRT 系统 DLL（含 RstrtMgr、WS2_32），未见需额外附带的 MinGW DLL；仍需干净系统实际启动验证。未把数据库、个人配置、私钥或 OCR 模型嵌入/附加。

源码预演为 649 个白名单文件（后续以最终 SOURCE_MANIFEST.json 为准），不带 .git/私人交接/内部审计/任务书/旧设计与输出/订单。独立构建得到上方同一 EXE。最终源码和二进制审查 ZIP 的哈希另存 `dist/oss-review/SHA256SUMS.txt`，避免报告自身快照形成哈希循环。V1 和 V2 均尚未获公开分发批准；正式签名后需重算 EXE/ZIP 哈希。

没有生成/上传生产私钥，没有替所有者购买证书或绕过签名。

## 8. 阻断与最终确认

| 优先级 | 未关闭事项 | 关闭条件 |
| --- | --- | --- |
| 已决定 | 项目许可/署名/商业公开范围 | Apache-2.0，ClipVault contributors，全自有源码和文档开放；第三方和素材真实权属不因署名改变 |
| 已完成 | 发布分支排除内部历史材料 | 50 个路径的历史版本已排除；126 个提交的保留文件内容、模式与演进关系均已验证。原 codex 分支仍为 2efd52d，仅发布分支允许作为 main 候选 |
| EXE 发行前 | 原生组件与运行库分发声明缺口 | 核对实际链接/随包分发组件并补适用声明；不作为仅上传自有源码及 go.mod/go.sum 的同一阻断项 |
| 待核实 | Substrait 模块归档缺许可原文 | 追溯许可来源；不将下载包缺文件直接等同于侵权或源码禁止公开，实际复制/再分发时另核查 |
| 稳定版发行前 | Windows 关键发行验收缺失 | 干净系统原生托盘、复制保存恢复、重启、备份恢复、数据完整性及剩余跳过项闭环；不以此阻止标明验证边界的源码公开 |
| 条件风险 | 联系方式统一 foobar | 维护者已指定占位，不再次索要个人联系方式；尚无已验证私下渠道，不计为渠道建立完成 |
| 条件风险 | Excelize 为精确预发布修复提交；Garble、签名、搬移故障、容量未验证 | 审核版本及回归证据，明确正式普通/混淆构建与签名策略，完成所选路径验收 |
| 已核实/待填 | GitHub 所有者已核实为 light8771；私人申请身份、ChatGPT 邮箱、Org ID 待填 | 账号经维护者授权登录并核实；私人申请字段由本人提供，不进公开仓库 |

建议首个候选版本 **v0.1.0-rc.1**，系统验收前不标 stable。预计文件：经批准源码+LICENSE、Windows x64 ZIP（EXE、许可/声明、使用/隐私说明）、SHA256SUMS、真实发布说明。现无验证安装器，不虚构安装包。当前 EXE 是开发公钥，须先定正式激活/分发策略。

申请依据 [OpenAI 官方表单](https://openai.com/zh-Hans-CN/form/codex-for-oss/) 和 [项目说明](https://developers.openai.com/community/codex-for-oss)；没有公开 stars、下载或外部使用数据，不声称广泛采用，不承诺资格/通过。Go 安全更新依据 [官方发布历史](https://go.dev/doc/devel/release#go1.26)。

## 9. 本轮发布分支复核

在发布分支检查点 8d1350f 独立克隆 127 个提交；50 个排除路径无历史条目，64 个内部独有 blob 对象不存在，克隆仅包含发布分支引用，全部对象均可达。原 codex 分支仍为 2efd52d，本地 13 份原先在保存点跟踪的内部文件仍存在。149 份上游声明原始哈希一致；453 个构建输入与保存点相同。独立克隆用原发布脚本和开发公钥构建，退出 0；EXE SHA-256 为 18938bc214698d1b266d2482573d755ce5aa9555f48d4655a305cd09279bac58。申请长度 420 / 383 / 462 校验通过，git fsck 退出 0。详细范围见 OSS_HISTORY_CLEANUP.md；逐提交映射和本地复核证据留在忽略的审查目录，不随源码上传。

本轮只进行了历史、文件、对象、声明哈希与独立构建复核，未重跑全量/竞态或实机测试。源码上传候选已经准备；这不代表正式二进制发行通过。未来只推送发布分支至 main，不能推送原分支或全部本地引用。

## 10. 下一步

许可、署名、商业范围和 foobar 占位已经记录，无需再次选择。发布分支历史清理已落实，源码上传应只使用 codex/oss-release-preparation；原分支与本地其他引用不可上传。维护者现已授权完成 GitHub 源码发布，登录账号核实为 light8771，目标仓库为 https://github.com/light8771/clipvault；实际上传、公开克隆和 CI 结果另行记录。EXE 分发声明、正式激活策略及 Windows 稳定版验收独立推进，不阻止仅源码候选。仅推送清理后的源码发布分支，不发布 EXE/Release；OpenAI 申请只备草稿。

## 11. GitHub 源码发布与公开克隆

2026-10-21，维护者授权其登录 GitHub、由 Codex 完成其余源码发布操作。已核实账号 light8771，新建公开仓库 https://github.com/light8771/clipvault，仅将 codex/oss-release-preparation 映射为远端 main；远端分支核对只有 main，Tag 和 Release 数量均为 0。原本地 origin 未更改，新增 github 远端；原 codex 分支仍为 2efd52d。未上传 EXE、用户数据、订单、内部资料、其他分支或 stash；没有提交 OpenAI 申请。

公开匿名克隆检查点 47fc26f 共 129 个提交，全部历史中 50 个排除路径的条目为 0，所有克隆对象均由公开引用可达，原始开发保存提交不存在；149 份上游声明原始哈希一致，git fsck 退出 0。随后修复 Windows 检出换行问题，在 925e0b8 再次以 core.autocrlf=true 匿名克隆，304 个 Go 文件 gofmt 检查均通过，go mod verify 退出 0，原发布脚本使用开发公钥构建退出 0。该公开克隆 EXE 的 SHA-256 为 e169a03fb32ebb8247533f1e1bbcb5697183a0e59af5d0b90c4710d4fbe67b96，仅本地审查，不上传。申请草稿字段现为 420 / 383 / 466 字符，校验通过。

首轮 GitHub CI 39576221184 在格式步骤失败：Windows 默认检出将 304 个 Go 文件转换为 CRLF。已添加 Go 源码 LF 属性，并在实际 Windows 公开克隆中验证；未改动业务源码、测试逻辑或放宽检查。第二轮 CI [39576673550](https://github.com/light8771/clipvault/actions/runs/39576673550) 通过工具链、格式、前端与订单脚本步骤，但在 DuckDB 静态库链接阶段失败，缺少 __emutls 符号；使用的是 MSYS2 GCC 16.2.0。已核实本机实际成功工具链为 Winlibs GCC 16.1.0 POSIX / MinGW-w64 14.0.0 UCRT r3，工作流改为同一固定上游 ZIP，校验 SHA-256。Go 普通与竞态分成独立步骤，各保留完整用例并给予 30 分钟超时，作业上限覆盖两轮和构建。下一轮真实 CI 结果另行追加，不能将之前的部分成功算作全量通过。
