"""从当前锁定依赖复制原始许可证，保留上游文字，不给项目本身授权。"""
import hashlib
import json
from pathlib import Path
import re
import subprocess


def main():
    root = Path(__file__).resolve().parents[1]
    raw = subprocess.check_output(["go", "list", "-m", "-json", "all"], cwd=root).decode()
    decoder = json.JSONDecoder()
    modules = []
    while raw.strip():
        module, end = decoder.raw_decode(raw.lstrip())
        modules.append(module)
        raw = raw.lstrip()[end:]
    target = root / "docs/third-party"
    target.mkdir(parents=True, exist_ok=True)
    rows = []
    license_name = re.compile(r"(?i)^(?:licen[cs]e|notice|copying|copyright|patents)(?:\.[a-z0-9_-]+)?$")
    for module in modules:
        if module.get("Main"):
            continue
        if not module.get("Dir"):
            fetched = json.loads(subprocess.check_output(["go", "mod", "download", "-json", module["Path"] + "@" + module["Version"]], cwd=root))
            module["Dir"] = fetched["Dir"]
        source = Path(module["Dir"])
        identifier = module["Path"] + "@" + module["Version"]
        slug = re.sub(r"[^A-Za-z0-9._-]", "_", identifier)
        copied = []
        for path in sorted(source.rglob("*")):
            if path.is_file() and license_name.fullmatch(path.name) and path.suffix.lower() not in {".go", ".py", ".js"}:
                relative = path.relative_to(source)
                data = path.read_bytes()
                destination = target / slug / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(data)
                copied.append({"path": destination.relative_to(root).as_posix(), "sha256": hashlib.sha256(data).hexdigest()})
        if not copied:
            print("需补充上游仓库级许可：", identifier)
        rows.append({"module": module["Path"], "version": module["Version"], "licenses": copied})
    for label, directory in [("Go", Path(subprocess.check_output(["go", "env", "GOROOT"]).decode().strip())), ("Acorn", root / "node_modules/acorn")]:
        copied = []
        for name in ("LICENSE", "PATENTS", "AUTHORS"):
            path = directory / name
            if path.is_file():
                data = path.read_bytes()
                destination = target / label / name
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(data)
                copied.append({"path": destination.relative_to(root).as_posix(), "sha256": hashlib.sha256(data).hexdigest()})
        if not copied:
            raise RuntimeError(label + " 许可证缺失")
        rows.append({"module": label, "version": subprocess.check_output(["go", "env", "GOVERSION"]).decode().strip() if label == "Go" else json.loads((directory / "package.json").read_text())["version"], "licenses": copied})
    (target / "inventory.json").write_text(json.dumps(rows, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    lines = ["# 锁定依赖的原始许可清单", "", "由 `python scripts/collect-third-party.py` 生成。包含模块图中的测试/工具/非 Windows 依赖，不表示全部链接进 EXE。", "", "| 模块 | 版本 | 原始许可与声明 |", "| --- | --- | --- |"]
    for row in rows:
        links = ["[" + Path(item["path"]).name + "](" + Path(item["path"]).relative_to("docs/third-party").as_posix() + ")" for item in row["licenses"]] or ["**模块缓存缺失，须补充上游仓库级许可**"]
        lines.append("| " + row["module"] + " | " + row["version"] + " | " + ", ".join(links) + " |")
    (target / "README.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print("许可来源数：", len(rows), "；原文文件数：", sum(len(row["licenses"]) for row in rows))


if __name__ == "__main__":
    main()
