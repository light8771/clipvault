# ClipVault 首次公开发布计划（保留历史，不执行上传）

2026-10-21 决定：Apache-2.0、署名 ClipVault contributors，以 codex 分支源码演进历史作为 GitHub main；联系方式 foobar 占位。不采用无历史仓库方案。维护者随后明确：历史中的内部日志、截图、审计资料等全部不公开。当前未获“批准公开发布 ClipVault”，下述命令仅为未来步骤。

## 最新排除要求（优先于下方原哈希方案）

内部日志、内部审计截图与文档、私人交接/任务上下文、发布探针和本地临时辅助材料，及其历史版本，均不纳入公开内容。本地原件和原始仓库保留，不删除用户数据。

目标仍是保留源码的演进历史，而不是创建只有一个初始提交的仓库。但已进入旧提交的私密材料，不能通过新增删除提交或 .gitignore 从历史中排除；涉及的提交及其后代哈希必须变化。因此，下面“原哈希保留、直接推送原 codex 分支”的旧方案暂停，不可照抄执行。

后续应先形成明确排除清单，在隔离发布副本中准备清理方案并验证所有待公开引用和对象；原仓库保持不变。本轮仅记录排除要求，未改写任何 Git 历史；依首次公开发布约束，历史清理及公开操作仍待明确授权。原始分支不能直接推送至 GitHub main。

## 1. 分支与历史范围

当前分支 `codex/clipvault-modular-refactor`，基线 f61c7c8，祖先提交 125 个。所有本地引用可达 128 个，另 3 个只在其他分支/暂存引用中；它们继续保留本地，不能用 --mirror/--all 自动公开。

**原方案（已暂停）：将 codex 分支原始完整提交链推送为远端 refs/heads/main。** 最新排除要求生效后，必须先解决旧提交内的内部材料，不能直接发布原始 125 个提交。现有本地 origin 不改，开源准备改动目前未提交。

“保留 codex 全部历史”与“把所有其他分支和 stash 也公开”不同。若还需公开另外 3 个提交，应分别检查来源和私密内容，再选择辅助分支名称；本轮不删除、不合并、不上传它们。

## 2. 完整历史的公开审核

已知 codex 历史含 logs/2026-07-09.log、内部审计图文；当前还跟踪 .audit/、.release-*.ps1、.sync-probe。正则扫描未发现高识别度凭据，不等于日志/截图已经可公开。

- 审核该分支所有版本的日志、截图、个人资料、签发/测试材料，以及旧版本依赖和复制代码的许可；只审查最新文件或当前 go.mod 不足以覆盖完整历史。
- 当前不需要的辅助文件可以在新提交中取消跟踪并保留本地原文件，但旧提交仍能访问，不能称为历史清理。
- 如果发现真实秘密或不可公开资料，**原哈希完全保留**与**从旧历史移除该内容**无法同时满足。届时先停止发布并处置凭据，报告具体位置；未经另行授权不改历史、不 force push。
- Git 作者姓名/邮箱及实际提交日期将随历史公开，不匿名改写、不伪造贡献记录。LICENSE 只授权有权许可的项目源码/文档，不授权泄露私人材料。

文件审查 ZIP 仍可用于对比当前源码和构建，并不替代完整 Git 历史，也不用于创建无历史仓库。旧 ZIP 是先前决策的记录，更新包必须带 LICENSE/NOTICE 并标明本地审阅状态。

## 3. 仅在阻断项关闭且批准公开后执行

先检查完整跟踪文件、拟提交改动和所有将推送的历史，排除内部材料，确认自有许可证及实际纳入仓库的第三方内容声明。源码依赖由 go.mod/go.sum 管理，不因原生 EXE 分发声明或 Windows 实机验收尚未完成，就一概阻止仅源码上传；这些项目在提供二进制和稳定版前分别关闭。不要直接 git add . 把内部材料混入；使用审核后的明确路径提交，保留其他工作区修改。

```powershell
# 未来人工步骤，本轮不执行。先确认当前确为 codex 分支。
git branch --show-current
git status --short
git diff --check
# 审核后逐路径 git add；以下占位符不能直接执行。
git add -- <逐项审核后的文件路径>
git diff --cached --stat
git diff --cached --check
git commit -m '准备首次开源：许可、审查文档和发布验证'
git log codex/clipvault-modular-refactor --oneline
```

确认 GitHub 所有者、目标名称未被占用；建议名称 clipvault。私有上传同样是对外发送源码，必须获得授权；不覆盖已有仓库或远端。

```powershell
$Repo = '<OWNER>/clipvault'
gh auth status
# 仅在批准且目标不存在时创建私有候选；不自动推送。
gh repo create $Repo --private
# 确认 github 远端名未占用，再新增，原 origin 保持原值。
git remote add github "https://github.com/$Repo.git"
# 只推所选分支完整历史，明确映射为 main。
git push -u github codex/clipvault-modular-refactor:refs/heads/main
gh repo edit $Repo --default-branch main
```

后续继续使用明确 refspec；本地 codex 名称与远端 main 不同，默认 push.simple 可能要求显式指定。需要本地改名时另作普通分支重命名，不因此重写历史。

## 4. 转公开与 Release

源码仓库转 public 前再次展示**最终跟踪文件清单、main 全部待公开提交及对象审查摘要**，确认内部材料已从待公开历史排除，实际纳入的第三方内容保留适用声明；如实说明 CI 和 Windows 验证的已完成与未完成项。EXE 分发声明及稳定版实机验收另在下方 Release 阶段关闭。foobar 仅占位，不虚构已建立私下安全渠道；可在获准建立仓库后配置 GitHub 私下漏洞报告。公开账户信息在私人申请副本中填写。

```powershell
# 再次完成最终审核并获批准后：
gh repo edit $Repo --visibility public --accept-visibility-change-consequences
$Tag = 'v0.1.0-rc.1'
git tag -a $Tag -m 'ClipVault 首个公开候选版'
git push github "refs/tags/$Tag"
gh release create $Tag --repo $Repo --draft --prerelease --title 'ClipVault 0.1.0 RC1' --notes-file '<审核后的发布说明>'
gh release upload $Tag '<验证并批准的 Windows ZIP>' '<SHA256SUMS.txt>' --repo $Repo
```

执行前核对 gh 对应版本的 --help。正式包包含 EXE、LICENSE、NOTICE、适用第三方原始声明及使用/隐私说明。先签名再打包再计算哈希；不分发开发凭据、用户数据或 OCR 模型。没有已验证安装器就不提供安装器。当前开发公钥/未签名审查 EXE 不作为正式稳定版。

## 5. 公开后验证及回滚边界

从真实公开 URL 在干净 Windows 10/11 x64 克隆 main，按 README 验证构建、原生剪贴板、签名/哈希、CI 和模板；保留历史 SHA。当前本地构建预演不算公开克隆成功。

把真实 URL、维护权限与可核查公开指标填到私人申请副本，不伪造 stars/下载量，申请由本人提交。删除 Release、Tag 或改回私有不能保证回收已克隆/缓存的完整历史；公开前必须完成历史审核，不能靠事后 force push 代替。
