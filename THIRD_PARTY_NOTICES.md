# 第三方组件及声明

ClipVault 自有源码和文档采用 Apache-2.0，署名见根目录 NOTICE。本文件及 `docs/third-party/` 保留第三方已经授予的许可，不对其重新授权。

## 源码仓库与二进制分发

项目使用 Go modules，通过 go.mod/go.sum 声明依赖，构建者自行下载；当前源码未采用 vendor 方式附带整个依赖源码。下方收集清单用于记录来源和辅助二进制发行，不表示必须把整个模块图的许可文件打包后才能上传自有源码。实际复制进仓库的第三方内容应保留适用声明；仅因某个模块下载包缺 LICENSE，不直接推定源码发布被禁止。Git 历史是否纳入过其他第三方内容，另按实际待公开版本核查。

发布编译好的 EXE 时，应核查实际链接及随包分发组件的许可与声明。Go modules 管理依赖下载和版本，不自动替发行方完成适用声明的保留；不能把我们的分发义务一概转给接收者。下方原生库缺口针对二进制发行。

完整锁定模块图（含测试、工具和其他平台模块）、版本、逐文件 SHA-256 与上游原始 LICENSE/NOTICE 见 [许可清单](docs/third-party/README.md) 和 [机器清单](docs/third-party/inventory.json)。通过 `python scripts/collect-third-party.py` 从 Go 模块缓存及 npm 锁定的 Acorn 复制，未改写上游原文。更新依赖后需重新生成，并人工检查移除过期目录。

主要直接组件：DuckDB Go v2.10504.0 及 bindings v0.10504.0、Google jsonschema-go v0.4.3、MCP Go SDK v1.7.0、Excelize（精确版本以 go.mod 为准）、Go x/crypto、x/net、x/sys、x/text。原始授权涵盖 MIT、BSD 类及 Apache-2.0 等，另有模块图中的 `github.com/hashicorp/golang-lru/v2` 使用 MPL-2.0；不能只按此概括表免除逐组件义务。Apache Arrow 的 NOTICE 及嵌套压缩库声明也已保留。

当前清单覆盖 107 个许可来源（105 个 Go 模块、Go 工具链和 Acorn）。`github.com/substrait-io/substrait-protobuf/go@v0.78.1` 的模块归档不含许可证，已在清单中明确留缺；不能据其生成代码猜测许可，须向上游取得对应版本的仓库级原文。这个缺口及原生库内嵌第三方声明未关闭前，不宣称完成全部第三方授权验收。

Go 标准库授权原文已收集。Acorn 8.18.0 为仅开发检查使用的 MIT 组件，不是浏览器运行依赖。前端采用原生模块、系统字体及代码内图形；没有发现附带第三方字体文件或 OCR 模型包。图标来自仓库绘图脚本的线索不等于已核实所有权。

发布 EXE 会包含 DuckDB 原生静态库。Go module 缓存中的顶层 MIT 声明**不足以证明已覆盖原生库内全部第三方组件**；上游构建来源、内嵌组件清单及适用版权声明仍需核实。同样要核对 MinGW/UCRT 链接与运行库，不要随意附带整个 MinGW 目录。正式二进制发行在此闭环前仍被阻断。Authenticode 签名、Garble/资源生成工具与平台组件的授权也各自独立。

分发时保留适用 LICENSE/NOTICE 原文；不要分发用户自定义背景、数据库、邮件附件、签发密钥或调试工具链。
