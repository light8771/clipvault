"""生成仅供文件审阅的白名单 ZIP；Git main 使用已排除内部资料的发布分支历史。"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import zipfile

import importlib.util


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, help="仅允许 dist/ 内尚不存在的 ZIP 路径")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    destination = (root / args.output).resolve()
    if not destination.is_relative_to(root) or not destination.is_relative_to((root / "dist").resolve()) or destination.suffix.lower() != ".zip":
        raise ValueError("输出必须是 dist/ 内的新 ZIP 文件")
    roots = ("cmd/", "internal/", "scripts/", "winres/", ".github/", "docs/")
    root_files = {".gitignore", ".gitattributes", "AGENTS.md", "README.md", "README.en.md", "LICENSE", "NOTICE", "CONTRIBUTING.md", "SECURITY.md", "PRIVACY.md", "CHANGELOG.md", "THIRD_PARTY_NOTICES.md", "TODO.md", "go.mod", "go.sum", "package.json", "package-lock.json", "gen_icon.py"}
    signing = {"signing/.gitignore", "signing/README.md", "signing/build-for-order.ps1", "signing/issue-license.ps1", "signing/cv-dev.ed25519.pub"}
    inventory = set()
    for flags in [("--cached",), ("--others", "--exclude-standard")]:
        raw = subprocess.check_output(["git", "ls-files", "-z", *flags], cwd=root)
        inventory.update(p.decode("utf-8") for p in raw.split(b"\0") if p)
    spec = importlib.util.spec_from_file_location("oss_audit", root / "scripts/audit-oss.py")
    scanner = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(scanner)
    files = []
    for relative in sorted(inventory):
        if relative.startswith("docs/ai-handoff/"):
            continue
        if relative not in root_files and relative not in signing and not relative.startswith(roots):
            continue
        path = root / relative
        if not path.is_file() or path.is_symlink() or any(p.is_symlink() or p.is_junction() for p in path.parents if p != root.parent):
            raise ValueError("快照路径缺失或经过链接：" + relative)
        # 原始上游许可文件可以没有扩展名，但敏感文件名不得混入。
        if scanner.SENSITIVE.search(relative) or path.suffix.lower() in {".exe", ".dll", ".zip", ".7z", ".dmp"}:
            raise ValueError("禁止纳入的文件类型：" + relative)
        allowed_suffixes = {".go", ".s", ".h", ".js", ".mjs", ".css", ".html", ".json", ".md", ".ps1", ".py", ".rc", ".ico", ".png", ".syso", ".yml", ".yaml", ".pub", ".mod", ".sum"}
        upstream_notice = relative.startswith("docs/third-party/") and path.name.lower().startswith(("licen", "notice", "copying", "copyright", "patents", "authors"))
        if path.suffix.lower() not in allowed_suffixes and path.name != ".gitignore" and relative not in {"LICENSE", "NOTICE"} and not upstream_notice:
            raise ValueError("未明确批准的快照文件类型：" + relative)
        data = path.read_bytes()
        blocked = [m for m in scanner.findings(data) if m["category"] in {"私钥标记", "GitHub令牌", "API令牌候选", "激活码候选"}]
        if blocked:
            raise ValueError("凭据候选须人工处理：" + relative)
        files.append((relative, data))
    required = {"README.md", "go.mod", "go.sum", "cmd/clipvault/main.go", "scripts/build-release.ps1"}
    if not required.issubset({p for p, _ in files}):
        raise ValueError("缺少必需源码")
    manifest = {"status": "local file review only; publication NOT approved", "publication_strategy": "sanitized codex/oss-release-preparation history as GitHub main", "project_license": "Apache-2.0", "baseline": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root).decode().strip(), "project_license_present": "LICENSE" in {p for p, _ in files}, "files": [{"path": p, "sha256": hashlib.sha256(data).hexdigest()} for p, data in files]}
    destination.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(destination, "x", compression=zipfile.ZIP_DEFLATED) as archive:
        for relative, data in files:
            info = zipfile.ZipInfo(relative, date_time=(2026, 10, 21, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(info, data)
        archive.writestr("SOURCE_MANIFEST.json", json.dumps(manifest, ensure_ascii=False, indent=2))
    print("白名单文件数：", len(files), "；附带 SOURCE_MANIFEST.json；不含 Git 历史。")
    print("SHA256:", hashlib.sha256(destination.read_bytes()).hexdigest())


if __name__ == "__main__":
    main()
