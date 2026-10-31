#!/usr/bin/env python3
"""Assert the official golangci-lint config still enables gosec at high/high.

Extracted verbatim from .github/workflows/custom-security.yml so the local
Gitea pipeline enforces the same gate.
"""
import pathlib
import sys

cfg = pathlib.Path("backend/.golangci.yml")
if not cfg.is_file():
    sys.exit("::error::找不到 backend/.golangci.yml")

lines = cfg.read_text(encoding="utf-8").splitlines()

# 只在 linters: 块内查找 enable 列表（formatters: 下也有 enable:）
in_linters = False
enabled = []
for idx, line in enumerate(lines):
    if line.startswith("linters:"):
        in_linters = True
        continue
    if in_linters and line and not line[0].isspace() and line.rstrip().endswith(":"):
        in_linters = False
        continue
    if in_linters and line.rstrip() == "  enable:":
        j = idx + 1
        while j < len(lines) and lines[j].startswith("    - "):
            enabled.append(lines[j][6:].strip())
            j += 1
        break

if not enabled:
    sys.exit("::error::未在 backend/.golangci.yml 的 linters 段找到 enable 列表")

if "gosec" not in enabled:
    sys.exit(
        "::error::官方 .golangci.yml 已不再启用 gosec —— "
        "本仓库的 Go 静态安全门禁会因此形同虚设。"
        "如确需移除，必须在本工作流给出替代方案并在 docs/SECURITY_REVIEW.md 登记理由。"
    )
print(f"gosec 已启用 ✓（linters.enable 共 {len(enabled)} 项：{', '.join(enabled)}）")

# 提取 linters.settings.gosec 块（到下一个 4 空格缩进的同级键为止）
blob = "\n".join(lines)
pos = blob.find("\n    gosec:")
if pos < 0:
    sys.exit("::error::官方 .golangci.yml 缺少 linters.settings.gosec 段")
block = []
for line in blob[pos + 1:].splitlines():
    if line.startswith("    ") and not line.startswith("      ") and block:
        break
    block.append(line)
block_text = "\n".join(block)

if "severity: high" not in block_text or "confidence: high" not in block_text:
    sys.exit(
        "::error::官方 gosec 配置不再是 severity=high + confidence=high。"
        "门禁语义已改变，请复核 docs/SECURITY_REVIEW.md §6 后同步更新本断言。"
    )
print("gosec 门禁语义确认 ✓ severity=high, confidence=high")
print("--- linters.settings.gosec ---")
print(block_text)
