# ClipVault GitHub 发布计划（仅源码候选，尚未上传）

2026-10-21：维护者已选择 Apache-2.0、ClipVault contributors 署名、foobar 联系方式占位，授权在独立发布分支清理内部材料，并随后授权由 Codex 完成 GitHub 源码发布。已核对登录账号 light8771，目标为 https://github.com/light8771/clipvault。仅源码仓库与 EXE 正式发行分开验收；没有获得发布 EXE/Release 或提交申请的授权。以下通用命令留作后续操作参考，实际上传和 CI 结果以执行报告为准。

## 当前分支和历史

原开发分支 `codex/clipvault-modular-refactor` 固定在 `2efd52d`，不再改写或移动。发布分支为 `codex/oss-release-preparation`，从原 126 个提交逐一排除内部资料，保留源码演进、父子关系、作者、提交者、日期和消息；相关哈希变化。后续只在发布分支新增普通提交。

已排除 50 个历史路径，包括内部日志和审计图文、AI 工具目录、旧内部开发计划与验收材料、评审报告、探针和临时产物。逐提交保留文件的 blob 和模式比对一致；原始历史与本地私人原件保留。证据范围见 OSS_HISTORY_CLEANUP.md。

**禁止直接推送原 codex 分支，禁止 --mirror、--all、打包 .git 或推送本地其他分支/stash。** 旧对象仍存在于本地，只有发布分支的可达对象适合作为源码候选。原本地 origin 保持不变。

## 源码上传前复核

- 在干净目录用 `--no-local --single-branch --no-tags` 仅克隆发布分支，核对全部历史版本与可达对象；不能用普通本地克隆复制全部对象来冒充隔离验证。
- 确认排除路径无历史树条目，原分支保持 2efd52d；说明正则扫描的边界，不宣称不存在一切未知秘密。
- 保留 LICENSE、NOTICE 与实际纳入仓库的第三方声明。go.mod/go.sum 由 Go modules 管理依赖，不要求把整个依赖源码或整个模块图许可搬入源码仓库。
- 如实列明已有全量/竞态测试、源码构建及未完成验收；CI 尚未在 GitHub 上运行时不能写为通过。
- 确认 GitHub 所有者及目标仓库 URL，没有批准前不查询私人凭据、不创建仓库、不上传源码。实际账户字段不使用 foobar 冒充；foobar 仅为安全/支持信息占位。

本地预演已在检查点 8d1350f 完成：127 个提交仅发布分支克隆、50 个排除路径无历史条目、64 个内部独有 blob 未进入对象库、原分支未带入；独立构建及 git fsck 均退出 0。结果随后以普通文档提交记录，详见 OSS_HISTORY_CLEANUP.md。该预演不是已上传或 GitHub CI 已运行的证据。

## 获准后，仅上传发布分支

下列命令均为未来人工步骤，不能把本文件视为上传授权。先核对当时 `git` / `gh` 的帮助和目标仓库状态；不覆盖已有仓库或远端。

```powershell
$Repo = '<OWNER>/clipvault'
git branch --show-current
# 应为 codex/oss-release-preparation，且状态干净。
git status --short
git rev-parse codex/clipvault-modular-refactor
# 应仍以 2efd52d 开头。
gh auth status
# 只有获准且目标不存在时建立私有候选；私有上传同样需要授权。
gh repo create $Repo --private
# 先确认 github 远端名未占用；不改原 origin。
git remote add github "https://github.com/$Repo.git"
git push -u github codex/oss-release-preparation:refs/heads/main
gh repo edit $Repo --default-branch main
```

本地发布分支与远端 main 名称不同，后续仍使用明确 refspec。不要通过 force push 覆盖已有 main；远端存在时先核对其内容和授权。

转 public 前展示最终文件、全部待公开提交与对象复核摘要。GitHub CI 实际运行后记录真实结果，任何新失败先修复；联系方式按维护者指示保持 foobar，不虚构可用的私下报告渠道。

```powershell
# 仅在维护者明确批准公开并完成最终复核后执行。
gh repo edit $Repo --visibility public --accept-visibility-change-consequences
```

## EXE 与 Release 独立验收

源码公开不自动发布 EXE。当前本地 V2 是开发公钥、未签名的审查产物，不当作稳定版下载。正式二进制发行前核对实际链接/随包分发的原生组件声明、激活与公钥策略、Windows 10/11 干净系统及跨应用实机验收、备份恢复和数据完整性。没有已验证安装器就不提供安装器。

经批准的 EXE 随包包含 LICENSE、NOTICE、适用第三方声明、隐私/使用说明。选定签名策略后先签名、再打包、再计算 SHA-256；不附数据库、订单、签发私钥、口令、用户配置或 OCR 模型。不随意打包整个工具链。

```powershell
# 未来 Release 步骤；只有所选产物实际验收完成且获准时执行。
$Tag = 'v0.1.0-rc.1'
git tag -a $Tag -m 'ClipVault 首个公开候选版'
git push github "refs/tags/$Tag"
gh release create $Tag --repo $Repo --draft --prerelease --title 'ClipVault 0.1.0 RC1' --notes-file '<审核后的发布说明>'
gh release upload $Tag '<验证并批准的 Windows ZIP>' '<SHA256SUMS.txt>' --repo $Repo
```

## 上传后验证

从真实 GitHub URL 克隆 main，验证 README 构建步骤、完整历史排除结果、Issue 模板、CI 和适用下载。此前的本地克隆只能作为本地预演，不算公开克隆验证。

公开 URL、真实可核查指标、申请账号信息由维护者确认后填入私人申请副本；没有 stars/下载/外部用户数据就如实写暂无，不伪造贡献记录。OpenAI 申请仅准备，不代提交。

把仓库改回私有或删除 Release 无法保证回收已克隆/缓存的历史，因此不能以事后操作代替发布前复核。
