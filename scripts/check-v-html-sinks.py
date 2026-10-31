#!/usr/bin/env python3
"""v-html 使用点门禁（B2 审计项：危险动态 HTML 穷举）。

背景
----
`v-html` 会把字符串当作 HTML 注入 DOM，是 Vue 应用里最典型的 XSS 汇聚点。
上游已经有 DOMPurify / sanitizeSvg 等消毒手段，但**没有任何机制阻止新代码
直接写 `v-html="rawValue"`**——这类回归只能靠人眼 review 兜住，不可靠。

本脚本把「穷举」这件事变成可自动复跑的检查：

  1. 扫描 `frontend/src/**/*.vue`，列出所有 `v-html` 使用点；
  2. 对每个使用点所属文件，要求该文件**导入了已知的 HTML 消毒器**；
  3. 未导入消毒器、但经人工核实安全的文件，必须登记在
     `.github/v-html-allowlist.json`，并写明理由与复核日期；
  4. 出现既无消毒器、又不在白名单里的新 `v-html` → 阻断。

设计原则
--------
* **默认拒绝**：白名单是闭集，新文件必须显式登记。这样"漏掉一个"不会静默通过。
* **只检查、不改写**：不自动加消毒调用——那会掩盖调用方的真实意图。
* **可离线自检**：`--selftest` 用合成输入验证判定逻辑，不依赖仓库实际状态。

注意：本检查是"结构性"的（文件级），不是数据流级的。它无法证明被 `v-html`
绑定的变量确实经过消毒（例如消毒结果存到 A，v-html 绑的是 B）。因此它是
**回归护栏**，不是完整证明；完整结论见 docs/SECURITY_REVIEW.md 的人工审计。
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

# ---------------------------------------------------------------------------
# 常量
# ---------------------------------------------------------------------------

REPO_ROOT = Path(__file__).resolve().parent.parent
FRONTEND_SRC = REPO_ROOT / "frontend" / "src"
ALLOWLIST_PATH = REPO_ROOT / ".github" / "v-html-allowlist.json"

# 已知的 HTML 消毒器标识符。只要文件里出现其中之一，就认为该文件的 v-html
# 使用点处于消毒能力覆盖范围内（结构性判定）。
SANITIZER_PATTERN = re.compile(
    r"\b(DOMPurify|sanitizeSvg|sanitizeHtml|sanitizeHTML|escapeHtml|escapeHTML)\b"
)

# 匹配 v-html 绑定。同时覆盖 `v-html="x"` 与 `v-html='x'` 两种写法（支持跨行匹配）。
V_HTML_PATTERN = re.compile(
    r"""v-html\s*=\s*(?P<quote>["'])(?P<expr>[\s\S]*?)(?P=quote)"""
)
SCRIPT_STRING_PATTERN = re.compile(r"""(["'`])(?:\\[\s\S]|(?!\1)[^\\])*\1""")

# 允许的复核日期上限（超过即视为过期，需重新确认）。
MAX_REVIEW_AGE_DAYS = 365


@dataclass
class Sink:
    """一个 v-html 使用点。"""

    relpath: str
    lineno: int
    expr: str

    def __str__(self) -> str:  # pragma: no cover - 仅用于输出
        return f"{self.relpath}:{self.lineno}  v-html=\"{self.expr}\""


@dataclass
class Result:
    ok: bool
    violations: list[str] = field(default_factory=list)
    warnings: list[str] = field(default_factory=list)
    stats: dict = field(default_factory=dict)


# ---------------------------------------------------------------------------
# 采集
# ---------------------------------------------------------------------------


def iter_vue_files(src: Path) -> list[Path]:
    if not src.is_dir():
        return []
    return sorted(src.rglob("*.vue"))


def collect_sinks(src: Path) -> list[Sink]:
    """扫描所有 .vue 文件，收集 v-html 使用点（支持单行及跨行书写）。"""
    sinks: list[Sink] = []
    for path in iter_vue_files(src):
        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        rel = path.relative_to(REPO_ROOT).as_posix()
        for m in V_HTML_PATTERN.finditer(text):
            lineno = text[: m.start()].count("\n") + 1
            raw_expr = m.group("expr")
            # 规范化：去除首尾空白，并将跨行换行/连续空格折叠为单空格
            normalized_expr = " ".join(raw_expr.split())
            sinks.append(Sink(rel, lineno, normalized_expr))
    return sinks


def extract_script_content(text: str) -> str:
    """提取 Vue 文件中的 <script> / <script setup> 块内容，并剥离注释。"""
    scripts = re.findall(r"<script\b[^>]*>(.*?)</script>", text, flags=re.DOTALL | re.IGNORECASE)
    combined = "\n".join(scripts)
    # 剥离多行注释 /* ... */
    combined = re.sub(r"/\*.*?\*/", "", combined, flags=re.DOTALL)
    # 剥离单行注释 // ...
    combined = re.sub(r"//.*$", "", combined, flags=re.MULTILINE)
    return combined


def is_sanitizer_imported_or_declared(expected: str, script_clean: str) -> bool:
    """过滤字符串后检查消毒器名称；不证明其定义、来源或调用有效。"""
    # 模板字符串整体剥离，包括换行、转义反引号与插值内容；替换为空格，
    # 防止 DOM"x"Purify 拼接成新标识符。
    no_strings = SCRIPT_STRING_PATTERN.sub(" ", script_clean)

    # 2. 在剥离字符串后的代码中检查该消毒器标识符（作为独立 token）
    return bool(re.search(rf"\b{re.escape(expected)}\b", no_strings))


def file_has_sanitizer(path: Path) -> bool:
    try:
        text = path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False
    script = extract_script_content(text)
    return is_sanitizer_imported_or_declared("DOMPurify", script)


def is_sink_sanitized(expr: str, script_clean: str) -> bool:
    """逐使用点判定：表达式本身是否直接调用消毒器，或绑定的标识符在 script 块中经过消毒处理。"""
    # 1. 表达式直接调用已知消毒器，例如 `v-html="sanitizeSvg(item.iconSvg)"`
    if SANITIZER_PATTERN.search(expr):
        return True

    # 2. 提取表达式涉及的核心变量/函数标识符（如 `renderedDocument`、`renderMarkdown`）
    identifiers = re.findall(r"\b[A-Za-z_][A-Za-z0-9_]*\b", expr)
    no_strings = SCRIPT_STRING_PATTERN.sub(" ", script_clean)
    for ident in identifiers:
        if ident in {"item", "row", "file", "val", "data", "selectedAnnouncement"}:
            continue
        # 检查 script 中该标识符的定义或赋值是否与消毒器关联
        pattern = re.compile(
            rf"\b{re.escape(ident)}\b[\s\S]{{0,300}}?\b(DOMPurify|sanitizeSvg|sanitizeHtml|escapeHtml)\b"
        )
        if pattern.search(no_strings):
            return True

    return False


# ---------------------------------------------------------------------------
# 白名单
# ---------------------------------------------------------------------------


def load_allowlist(path: Path) -> dict:
    if not path.is_file():
        return {"sinks": [], "files": {}}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise SystemExit(f"::error::{path} 不是合法 JSON: {exc}") from exc
    if not isinstance(data, dict):
        raise SystemExit(f"::error::{path} 结构不符合预期")
    return data


# ---------------------------------------------------------------------------
# 判定
# ---------------------------------------------------------------------------


def evaluate(sinks: list[Sink], allowlist: dict, frontend_src: Path) -> Result:
    res = Result(ok=True)
    reg_sinks: dict[tuple[str, str], dict] = {
        (item["file"], item["expr"]): item
        for item in allowlist.get("sinks", [])
        if isinstance(item, dict) and "file" in item and "expr" in item
    }
    allow_files: dict = allowlist.get("files", {})

    found_sink_keys = set()
    cache_script_clean: dict[str, str] = {}

    for s in sinks:
        key = (s.relpath, s.expr)
        found_sink_keys.add(key)
        full = REPO_ROOT / s.relpath

        entry = reg_sinks.get(key)
        # 兼容旧版文件级白名单
        if entry is None and s.relpath in allow_files:
            entry = allow_files[s.relpath]

        # 1. 必须在基准清单中逐点登记
        if entry is None:
            res.ok = False
            res.violations.append(
                f"未登记的 v-html 使用点：{s}  （新增使用点必须在 {ALLOWLIST_PATH.name} 逐点登记并完成安全核实）"
            )
            continue

        # 2. 校验元数据
        owner = str(entry.get("owner", "")).strip()
        reviewed_on = str(entry.get("reviewed_on", "")).strip()
        sanitizer = str(entry.get("sanitizer", "")).strip()

        missing = [
            name
            for name, val in (("owner", owner), ("reviewed_on", reviewed_on))
            if not val
        ]
        if missing:
            res.ok = False
            res.violations.append(
                f"{s.relpath} 使用点 [{s.expr}] 登记缺少字段 {missing}"
            )
            continue

        if owner.startswith("<") or "your-domain" in owner or "TODO" in owner.upper():
            res.ok = False
            res.violations.append(
                f"{s.relpath} 使用点 [{s.expr}] 的 owner 仍是占位符：{owner!r}"
            )
            continue

        age = _days_since(reviewed_on)
        if age is None:
            res.ok = False
            res.violations.append(
                f"{s.relpath} 使用点 [{s.expr}] 的 reviewed_on={reviewed_on!r} 不是 YYYY-MM-DD"
            )
            continue

        if age > MAX_REVIEW_AGE_DAYS:
            res.ok = False
            res.violations.append(
                f"{s.relpath} 使用点 [{s.expr}] 登记已过期（reviewed_on={reviewed_on}，{age} 天 > "
                f"{MAX_REVIEW_AGE_DAYS} 天），请重新核实"
            )
            continue

        # 3. 校验实际代码形态
        if s.relpath not in cache_script_clean:
            try:
                raw_text = full.read_text(encoding="utf-8", errors="replace")
            except OSError:
                raw_text = ""
            cache_script_clean[s.relpath] = extract_script_content(raw_text)
        script_clean = cache_script_clean[s.relpath]

        if sanitizer.startswith("exemption:"):
            reason = str(entry.get("reason", "")).strip()
            if not reason:
                res.ok = False
                res.violations.append(
                    f"{s.relpath} 豁免使用点 [{s.expr}] 缺少 reason 理由说明"
                )
                continue
            res.warnings.append(
                f"{s.relpath}:{s.lineno} [{s.expr}] 属豁免使用点（{sanitizer}, owner={owner}）"
            )
        else:
            # 声明了具体消毒器（如 DOMPurify、sanitizeSvg、escapeHtml）
            # 必须在剥离注释与字符串字面量后的 script 块中真实存在该消毒器
            expected = sanitizer or "DOMPurify"
            if not is_sanitizer_imported_or_declared(expected, script_clean):
                res.ok = False
                res.violations.append(
                    f"使用点声明使用 {expected}，但在 script 块中未找到对应导入/定义（注释与字符串常量伪造无效）：{s}"
                )

    # 4. 检查失效/多余登记（防止白名单或清单残留）
    for key in sorted(reg_sinks):
        if key not in found_sink_keys:
            res.warnings.append(
                f"登记清单中的使用点已不存在：{key[0]} -> v-html=\"{key[1]}\"，建议从白名单移除"
            )

    res.stats = {
        "sinks": len(sinks),
        "files": len(set(s.relpath for s in sinks)),
        "registered": len(reg_sinks),
        "allowlisted": sum(1 for e in reg_sinks.values() if str(e.get("sanitizer", "")).startswith("exemption:")),
        "unsanitized": sum(1 for e in reg_sinks.values() if str(e.get("sanitizer", "")).startswith("exemption:")),
    }
    return res


def _days_since(date_str: str) -> int | None:
    import datetime as _dt

    try:
        d = _dt.date.fromisoformat(date_str)
    except (ValueError, TypeError):
        return None
    return (_dt.date.today() - d).days


# ---------------------------------------------------------------------------
# 自检
# ---------------------------------------------------------------------------


def selftest() -> int:
    """用合成输入验证判定逻辑（不依赖仓库实际状态）。"""
    import tempfile
    import datetime as _dt

    checks: list[tuple[str, bool]] = []
    recent = (_dt.date.today() - _dt.timedelta(days=10)).isoformat()

    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        src = root / "src"
        src.mkdir()

        # 1) 有消毒器且在 script 导入的文件
        (src / "Sanitized.vue").write_text(
            '<template><div v-html="x"></div></template>\n'
            "<script setup>\nimport DOMPurify from 'dompurify'\n</script>\n",
            encoding="utf-8",
        )
        # 2) 未登记的文件
        (src / "Raw.vue").write_text(
            '<template><div v-html="y"></div></template>\n', encoding="utf-8"
        )
        # 3) 豁免登记的文件
        (src / "Allowed.vue").write_text(
            '<template><div v-html="z"></div></template>\n', encoding="utf-8"
        )
        # 4) 试图用 template 注释 <!-- DOMPurify --> 绕过
        (src / "CommentBypass.vue").write_text(
            '<template>\n<!-- DOMPurify -->\n<div v-html="raw"></div>\n</template>\n'
            "<script setup>\nconst raw = 'test'\n</script>\n",
            encoding="utf-8",
        )
        # 5) 已有消毒器的文件新增裸使用点
        (src / "MixedSinks.vue").write_text(
            '<template>\n<div v-html="safe"></div>\n<div v-html="danger"></div>\n</template>\n'
            "<script setup>\nimport DOMPurify from 'dompurify'\n</script>\n",
            encoding="utf-8",
        )
        # 6) 跨行书写的 v-html
        (src / "Multiline.vue").write_text(
            '<template>\n<div v-html="\n  raw_multiline\n"></div>\n</template>\n'
            "<script setup>\nconst raw_multiline = 'test'\n</script>\n",
            encoding="utf-8",
        )
        # 7) 试图用 script 内普通字符串常量 "DOMPurify" 绕过
        (src / "StringFake.vue").write_text(
            '<template>\n<div v-html="fake_sanitized"></div>\n</template>\n'
            '<script setup>\nconst fake = "DOMPurify"\n</script>\n',
            encoding="utf-8",
        )

        global REPO_ROOT
        saved_root = REPO_ROOT
        try:
            REPO_ROOT = root
            sinks = collect_sinks(src)
            checks.append(("采集到全部 sink（包含跨行）", len(sinks) == 8))
            checks.append(
                ("跨行 sink 表达式规范化为 raw_multiline", any(s.expr == "raw_multiline" for s in sinks))
            )

            # 场景 A：仅合法登记 Sanitized.vue 和 Allowed.vue
            allowlist_base = {
                "sinks": [
                    {
                        "file": "src/Sanitized.vue",
                        "expr": "x",
                        "sanitizer": "DOMPurify",
                        "owner": "@y1yyum",
                        "reviewed_on": recent,
                    },
                    {
                        "file": "src/Allowed.vue",
                        "expr": "z",
                        "sanitizer": "exemption:test",
                        "reason": "test exemption",
                        "owner": "@y1yyum",
                        "reviewed_on": recent,
                    },
                ]
            }
            res_a = evaluate(sinks, allowlist_base, src)
            checks.append(("未登记的 raw v-html 被阻断", not res_a.ok))
            checks.append(
                ("阻断信息指向 Raw.vue", any("Raw.vue" in v for v in res_a.violations))
            )
            checks.append(
                ("CommentBypass.vue 未登记被阻断", any("CommentBypass.vue" in v for v in res_a.violations))
            )
            checks.append(
                ("已有消毒器文件中的未登记裸点 danger 被阻断",
                 any("MixedSinks.vue" in v and "danger" in v for v in res_a.violations))
            )
            checks.append(
                ("未登记的跨行 Multiline.vue 被阻断", any("Multiline.vue" in v for v in res_a.violations))
            )
            checks.append(
                ("合法登记且 script 有导入的 Sanitized.vue 未被阻断",
                 not any("Sanitized.vue" in v for v in res_a.violations))
            )

            # 场景 B1：试图将 CommentBypass 伪造登记为 DOMPurify，但 script 仅有注释
            fake_comment_allowlist = {
                "sinks": [
                    {
                        "file": "src/CommentBypass.vue",
                        "expr": "raw",
                        "sanitizer": "DOMPurify",
                        "owner": "@y1yyum",
                        "reviewed_on": recent,
                    }
                ]
            }
            res_b1 = evaluate([s for s in sinks if "CommentBypass" in s.relpath], fake_comment_allowlist, src)
            checks.append(("注释伪造 DOMPurify 被识别并阻断", not res_b1.ok))

            # 场景 B2：试图在 script 中通过普通字符串常量 "DOMPurify" 伪造真实导入
            fake_string_allowlist = {
                "sinks": [
                    {
                        "file": "src/StringFake.vue",
                        "expr": "fake_sanitized",
                        "sanitizer": "DOMPurify",
                        "owner": "@y1yyum",
                        "reviewed_on": recent,
                    }
                ]
            }
            res_b2 = evaluate([s for s in sinks if "StringFake" in s.relpath], fake_string_allowlist, src)
            checks.append(("普通字符串常量伪造 DOMPurify 被识别并阻断", not res_b2.ok))

            for label, literal in (
                ("模板字符串", "`DOMPurify`"),
                ("跨行模板字符串", "`first line\nDOMPurify\nlast line`"),
                ("含转义反引号的模板字符串", r"`escaped \` DOMPurify`"),
                ("字符串拼接标识符", 'DOM"x"Purify'),
                ("模板字符串拼接标识符", "DOM`x`Purify"),
            ):
                (src / "StringFake.vue").write_text(
                    '<template><div v-html="fake_sanitized"></div></template>\n'
                    f"<script setup>\nconst fake = {literal}\n</script>\n",
                    encoding="utf-8",
                )
                res_template = evaluate(
                    [s for s in sinks if "StringFake" in s.relpath],
                    fake_string_allowlist,
                    src,
                )
                checks.append((f"{label}伪造 DOMPurify 被识别并阻断", not res_template.ok))

            # 场景 C：登记但缺字段 / 占位符 owner → 应阻断
            bad_meta_allowlist = {
                "sinks": [
                    {
                        "file": "src/Allowed.vue",
                        "expr": "z",
                        "sanitizer": "exemption:test",
                        "reason": "test",
                        "owner": "<owner>",
                        "reviewed_on": recent,
                    }
                ]
            }
            res_c = evaluate([s for s in sinks if "Allowed" in s.relpath], bad_meta_allowlist, src)
            checks.append(("占位符 owner 被阻断", not res_c.ok))

            # 场景 D：全部 8 个 sink 均完整合法登记
            full_valid_allowlist = {
                "sinks": [
                    {"file": "src/Sanitized.vue", "expr": "x", "sanitizer": "DOMPurify", "owner": "@y1yyum", "reviewed_on": recent},
                    {"file": "src/Raw.vue", "expr": "y", "sanitizer": "exemption:raw", "reason": "ok", "owner": "@y1yyum", "reviewed_on": recent},
                    {"file": "src/Allowed.vue", "expr": "z", "sanitizer": "exemption:allowed", "reason": "ok", "owner": "@y1yyum", "reviewed_on": recent},
                    {"file": "src/CommentBypass.vue", "expr": "raw", "sanitizer": "exemption:cmt", "reason": "ok", "owner": "@y1yyum", "reviewed_on": recent},
                    {"file": "src/MixedSinks.vue", "expr": "safe", "sanitizer": "DOMPurify", "owner": "@y1yyum", "reviewed_on": recent},
                    {"file": "src/MixedSinks.vue", "expr": "danger", "sanitizer": "exemption:test", "reason": "ok", "owner": "@y1yyum", "reviewed_on": recent},
                    {"file": "src/Multiline.vue", "expr": "raw_multiline", "sanitizer": "exemption:multiline", "reason": "ok", "owner": "@y1yyum", "reviewed_on": recent},
                    {"file": "src/StringFake.vue", "expr": "fake_sanitized", "sanitizer": "exemption:fake", "reason": "ok", "owner": "@y1yyum", "reviewed_on": recent},
                ]
            }
            res_d = evaluate(sinks, full_valid_allowlist, src)
            checks.append(("全部合法登记 → 通过", res_d.ok))

            # 场景 E：登记已过期 → 应阻断
            expired_allowlist = {
                "sinks": [
                    {
                        "file": "src/Allowed.vue",
                        "expr": "z",
                        "sanitizer": "exemption:test",
                        "reason": "test",
                        "owner": "@y1yyum",
                        "reviewed_on": "2000-01-01",
                    }
                ]
            }
            res_e = evaluate([s for s in sinks if "Allowed" in s.relpath], expired_allowlist, src)
            checks.append(("过期登记被阻断", not res_e.ok))
        finally:
            REPO_ROOT = saved_root

    failed = [name for name, ok in checks if not ok]
    for name, ok in checks:
        print(f"  [{'通过' if ok else '失败'}] {name}")
    print(f"\n自检：{len(checks) - len(failed)}/{len(checks)} 通过")
    return 0 if not failed else 1


# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------


def main() -> int:
    parser = argparse.ArgumentParser(description="v-html 使用点门禁")
    parser.add_argument("--json-out", help="把结论写入指定 JSON 文件")
    parser.add_argument("--selftest", action="store_true", help="运行离线自检后退出")
    parser.add_argument("--list", action="store_true", help="仅列出所有 v-html 使用点")
    args = parser.parse_args()

    if args.selftest:
        return selftest()

    sinks = collect_sinks(FRONTEND_SRC)
    if args.list:
        for s in sinks:
            print(s)
        print(f"\n共 {len(sinks)} 个 v-html 使用点")
        return 0

    allowlist = load_allowlist(ALLOWLIST_PATH)
    res = evaluate(sinks, allowlist, FRONTEND_SRC)

    print(f"v-html 使用点：{res.stats['sinks']} 个，分布于 {res.stats['files']} 个文件")
    print(f"  其中无消毒器：{res.stats['unsanitized']} 个文件")
    print(f"  白名单登记：{res.stats['allowlisted']} 个文件")
    print()

    for w in res.warnings:
        print(f"::warning::{w}")
    for v in res.violations:
        print(f"::error::{v}")

    if args.json_out:
        payload = {
            "schema": "v-html-sink-gate/v1",
            "ok": res.ok,
            "stats": res.stats,
            "warnings": res.warnings,
            "violations": res.violations,
            "sinks": [{"file": s.relpath, "line": s.lineno, "expr": s.expr} for s in sinks],
        }
        Path(args.json_out).write_text(
            json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8"
        )

    if res.ok:
        print("结论：通过 —— 所有 v-html 使用点均处于消毒覆盖或已登记豁免之下。")
        return 0
    print(f"结论：阻断 —— 发现 {len(res.violations)} 处未受控的 v-html 使用点。")
    return 1


if __name__ == "__main__":
    sys.exit(main())
