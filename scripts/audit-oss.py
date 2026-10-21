"""只读本地开源扫描；只记录路径、行号、类别，绝不输出匹配值。

扫描所有引用可达 Git blob、索引和工作区（含忽略文件）；不读取用户数据目录。
输出是本地审查线索，不能替代人工权利审查、截图检查或凭据轮换。
"""
import argparse
import collections
import json
import os
from pathlib import Path
import re
import subprocess


def git(*args):
    return subprocess.check_output(["git", "-c", "core.quotePath=false", *args])


PATTERNS = {
    "私钥标记": rb"-----BEGIN (?:RSA |EC |OPENSSH |ENCRYPTED )?PRIVATE KEY-----",
    "GitHub令牌": rb"\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})\b",
    "API令牌候选": rb"\b(?:sk-[A-Za-z0-9_-]{24,}|AKIA[A-Z0-9]{16}|cv_[A-Za-z0-9_-]{43})\b",
    "激活码候选": rb"\bCV1\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}",
    "凭据赋值候选": rb'''(?i)(?:password|passwd|passphrase|smtpPassword|api[_-]?key|secret|access[_-]?token)["']?\s*[:=]\s*["'][^"'\r\n]{8,}["']''',
    "授权私钥封装候选": rb"clipvault-license-private-key-v1",
    "邮箱候选": rb"[A-Za-z0-9_.+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}",
}
RULES = {k: re.compile(v) for k, v in PATTERNS.items()}
SENSITIVE = re.compile(r"(?i)(\.duckdb(?:\..*)?|\.sqlite\w*|\.db|\.cvexport|\.pfx|\.p12|\.pem|\.key|\.lic|\.ed25519|\.private-key|\.log|\.env(?:\..*)?)$|(?:^|/)(?:key-passphrase[^/]*|clipvault\.(?:recovery|state))$")
MEDIA = re.compile(r"(?i)\.(?:png|jpe?g|gif|webp|bmp|ico|syso|woff2?|ttf|zip|7z|pdf|exe|dll)$")


def findings(data):
    result = []
    for category, rule in RULES.items():
        lines = sorted({data.count(b"\n", 0, m.start()) + 1 for m in rule.finditer(data)})
        if lines:
            result.append({"category": category, "lines": lines})
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default=".audit/oss-20261021/scan.json")
    args = parser.parse_args()
    root = Path(git("rev-parse", "--show-toplevel").decode().strip())
    os.chdir(root)
    report = {"scope": "all reachable refs + unreachable blobs + index + worktree including ignored files", "history": [], "unreachable": [], "index": [], "worktree": [], "errors": []}
    report["commits"] = int(git("rev-list", "--all", "--count"))
    report["head"] = git("rev-parse", "HEAD").decode().strip()
    report["remote_count"] = len(git("remote").splitlines())
    objects = git("rev-list", "--objects", "--all").decode("utf-8", "replace").splitlines()
    paths = {}
    for line in objects:
        oid, _, path = line.partition(" ")
        paths[oid] = path
    proc = subprocess.Popen(["git", "cat-file", "--batch-all-objects", "--batch"], stdout=subprocess.PIPE)
    blob_count = 0
    unreachable_count = 0
    while header := proc.stdout.readline():
        oid, kind, size = header.decode().strip().split()
        size = int(size)
        data = proc.stdout.read(size)
        assert len(data) == size and proc.stdout.read(1) == b"\n"
        if kind != "blob":
            continue
        if oid not in paths:
            unreachable_count += 1
            matches = findings(data)
            if matches:
                report["unreachable"].append({"blob": oid, "size": size, "matches": matches})
            continue
        blob_count += 1
        path = paths[oid]
        matches = findings(data)
        if matches or SENSITIVE.search(path) or MEDIA.search(path):
            report["history"].append({"path": path, "blob": oid, "size": size, "sensitive_name": bool(SENSITIVE.search(path)), "media": bool(MEDIA.search(path)), "matches": matches})
    assert proc.wait() == 0
    report["reachable_blobs_scanned"] = blob_count
    report["unreachable_blobs_not_in_publish_history"] = unreachable_count
    report["unreachable_blobs_scanned"] = unreachable_count
    index = git("ls-files", "--stage", "-z").split(b"\0")
    index_count = 0
    for entry in filter(None, index):
        meta, rawpath = entry.split(b"\t", 1)
        mode, oid, stage = meta.decode().split()
        path = rawpath.decode("utf-8", "replace")
        if mode == "160000":
            report["errors"].append({"path": path, "type": "submodule_requires_separate_audit"})
            continue
        data = git("cat-file", "blob", oid)
        index_count += 1
        matches = findings(data)
        if matches or SENSITIVE.search(path) or MEDIA.search(path):
            report["index"].append({"path": path, "matches": matches, "sensitive_name": bool(SENSITIVE.search(path)), "media": bool(MEDIA.search(path))})
    report["index_files_scanned"] = index_count
    scanned = 0
    excluded = collections.Counter()
    for directory, dirs, files in os.walk(root, followlinks=False):
        for name in list(dirs):
            p = Path(directory) / name
            # 外部安装依赖单独用锁文件、模块许可证和漏洞工具审计。
            if name in {".git", "node_modules"} or p.is_symlink() or p.is_junction():
                dirs.remove(name)
                excluded[name] += 1
        for name in files:
            p = Path(directory) / name
            relative = p.relative_to(root).as_posix()
            if relative.startswith(".audit/oss-20261021/"):
                continue  # 本轮日志可能正在被测试进程写入。
            try:
                if p.is_symlink():
                    report["errors"].append({"path": relative, "type": "symlink_not_read"})
                    continue
                size = p.stat().st_size
                matches = [] if MEDIA.search(relative) else findings(p.read_bytes())
                scanned += 1
                if matches or SENSITIVE.search(relative) or MEDIA.search(relative):
                    ignored = subprocess.run(["git", "check-ignore", "-q", "--", relative], capture_output=True).returncode == 0
                    report["worktree"].append({"path": relative, "size": size, "ignored": ignored, "sensitive_name": bool(SENSITIVE.search(relative)), "media": bool(MEDIA.search(relative)), "matches": matches})
            except OSError:
                report["errors"].append({"path": relative, "type": "unreadable"})
    report["worktree_files_scanned"] = scanned
    report["excluded_directories"] = dict(excluded)
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({k: report[k] for k in ["commits", "remote_count", "reachable_blobs_scanned", "unreachable_blobs_scanned", "index_files_scanned", "worktree_files_scanned", "errors"]}, ensure_ascii=False))
    print("脱敏扫描结果已保存；命中项必须人工分类，不代表已确认泄露。")


if __name__ == "__main__":
    main()
