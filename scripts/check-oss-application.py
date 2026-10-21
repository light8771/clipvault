"""核对申请三个说明字段的实际字符数及文档一致性。"""
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
fields = json.loads((root / "docs/CODEX_OSS_FIELDS.json").read_text(encoding="utf-8"))
assert set(fields) == {"eligibility", "api_usage", "additional"}
document = (root / "docs/CODEX_OSS_APPLICATION.md").read_text(encoding="utf-8")
for key, value in fields.items():
    assert isinstance(value, str) and value.isascii() and 0 < len(value) <= 500, key
    assert value in document, key + " 文档与实际字段不一致"
    print(f"{key}: {len(value)}/500 characters")
