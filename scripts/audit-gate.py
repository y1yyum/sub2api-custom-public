#!/usr/bin/env python3
"""前端依赖审计门禁（二开补充，不修改官方脚本）。

背景
----
官方 `tools/check_pnpm_audit_exceptions.py` 有一个**静默失效**盲区：

    它只遍历 `pnpm audit --prod` 的结果，并且**只对匹配到的漏洞**检查例外是否过期。

因此 `.github/audit-exceptions.yml` 中那些「已经不在生产依赖闭包内」的条目
既不会报缺失、也不会报过期 —— 它们完全不被校验，却仍在文件里冒充有效例外。

实测（运行 38623734609 的审计 Artifact）：

    package     advisory                    in prod  in full  expires_on
    xlsx        GHSA-4r6h-8v6p-xvw6         True     True     2027-01-06   ← 有效
    xlsx        GHSA-5pgg-2g8v-p4x9         True     True     2027-01-06   ← 有效
    lodash      GHSA-r5fr-rjxr-66jc         False    True     2026-07-02   ← 过期且不被校验
    lodash-es   GHSA-r5fr-rjxr-66jc         False    False    2026-07-02   ← 完全失效
    axios       GHSA-3p68-rc4w-qgx5         False    False    2026-07-10   ← 完全失效
    nanoid      GHSA-2v37-7h3g-55p8         False    False    2026-10-13   ← 完全失效

本脚本补上两个缺口：

  A. 例外清单有效性：`.github/audit-exceptions.yml` 的**每一条**都必须
     (A1) 匹配 prod 审计中的 high/critical 漏洞（否则是无效条目）
     (A2) 未过期（无论是否匹配）
     (A3) severity 与审计一致

  B. devDependencies 棘轮：官方只跑 `pnpm audit --prod`，devDependencies 无门禁。
     用基线文件记录「已接受」的 dev-only high/critical 集合：
     (B1) 出现基线外的新漏洞 → 阻断
     (B2) 基线中已被解决的条目 → 提示更新基线（不阻断）
     (B3) 基线超过复核日期 → 阻断（强制复核）

用法
----
  scripts/audit-gate.py \
      --prod   frontend/audit-prod.json \
      --full   frontend/audit-full.json \
      --exceptions .github/audit-exceptions.yml \
      --baseline   .github/audit-baseline-devdeps.json

退出码
------
  0  通过
  1  阻断
  2  用法或输入错误
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import sys
from datetime import date, datetime
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
OFFICIAL_CHECKER = REPO_ROOT / "tools" / "check_pnpm_audit_exceptions.py"


def load_official_checker():
    """复用官方解析逻辑，避免重复实现 200 行 pnpm audit 兼容代码。

    这是一次**有意的耦合**：宁可依赖官方实现（签名变化会立刻 ImportError/AttributeError
    使 CI 变红，属 fail-closed），也不要复制一份会与上游漂移的解析器。
    """
    if not OFFICIAL_CHECKER.exists():
        raise SystemExit(f"找不到官方检查器：{OFFICIAL_CHECKER}")
    spec = importlib.util.spec_from_file_location("_official_audit_checker", OFFICIAL_CHECKER)
    if spec is None or spec.loader is None:
        raise SystemExit(f"无法加载官方检查器：{OFFICIAL_CHECKER}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    for attr in ("iter_vulns", "parse_exceptions", "normalize_severity",
                 "normalize_package", "normalize_advisory", "HIGH_SEVERITIES"):
        if not hasattr(module, attr):
            raise SystemExit(
                f"官方检查器缺少预期接口 '{attr}'，本脚本需同步更新：{OFFICIAL_CHECKER}"
            )
    return module


def load_json(path: Path) -> dict:
    try:
        with path.open("r", encoding="utf-8") as handle:
            data = json.load(handle)
    except json.JSONDecodeError as exc:
        raise SystemExit(
            f"文件不是合法 JSON：{path}（{exc}）。"
            "若该文件来自 `pnpm audit --json`，请确认审计命令确实执行成功。"
        ) from exc
    if not isinstance(data, dict):
        raise SystemExit(f"文件顶层不是 JSON 对象：{path}")
    return data


def validate_audit_payload(data: dict, path: Path | str) -> dict:
    """验证 pnpm audit 输出结构的完整性与字段类型，区分扫描失败与零发现。"""
    if not isinstance(data, dict):
        raise SystemExit(f"::error::依赖审计报告顶层不是 JSON 对象：{path}")

    if "error" in data:
        err_info = data["error"]
        raise SystemExit(
            f"::error::依赖审计输出包含错误字段：{path}\n"
            f"错误详情：{err_info}\n"
            "说明 pnpm audit 命令执行失败（如网络中断、镜像源故障或参数错误），拒绝将扫描失败误判为零漏洞。"
        )

    # 结构完整性校验：必须包含漏洞明细字段（advisories 或 vulnerabilities 字典）
    has_advisories = "advisories" in data
    has_vulns = "vulnerabilities" in data

    if not (has_advisories or has_vulns):
        raise SystemExit(
            f"::error::依赖审计报告结构异常，缺少 advisories/vulnerabilities 漏洞明细字段：{path}\n"
            f"顶层键名：{list(data.keys())}。仅包含 metadata 统计而缺失明细属于不完整输出，拒绝将异常输出视为零漏洞通过。"
        )

    # 字段类型校验
    if has_advisories and not isinstance(data["advisories"], dict):
        raise SystemExit(
            f"::error::依赖审计报告字段类型异常，advisories 必须为字典：{path} (实际: {type(data['advisories']).__name__})"
        )
    if has_vulns and not isinstance(data["vulnerabilities"], dict):
        raise SystemExit(
            f"::error::依赖审计报告字段类型异常，vulnerabilities 必须为字典：{path} (实际: {type(data['vulnerabilities']).__name__})"
        )

    if "metadata" in data:
        if not isinstance(data["metadata"], dict):
            raise SystemExit(
                f"::error::依赖审计报告字段类型异常，metadata 必须为字典：{path} (实际: {type(data['metadata']).__name__})"
            )
        meta_vulns = data["metadata"].get("vulnerabilities")
        if meta_vulns is not None:
            if not isinstance(meta_vulns, dict):
                raise SystemExit(
                    f"::error::metadata.vulnerabilities 必须为字典：{path} (实际: {type(meta_vulns).__name__})"
                )
            for sev_key in ("info", "low", "moderate", "high", "critical"):
                val = meta_vulns.get(sev_key)
                if val is not None and not isinstance(val, int):
                    raise SystemExit(
                        f"::error::metadata.vulnerabilities.{sev_key} 必须为整数：{path} (实际: {type(val).__name__})"
                    )

            # 统计与明细的一致性校验：若 metadata 明确注明存在 high 或 critical 漏洞，明细绝不能全为空
            meta_high = meta_vulns.get("high", 0) or 0
            meta_critical = meta_vulns.get("critical", 0) or 0
            if (meta_high + meta_critical) > 0:
                adv_count = len(data.get("advisories", {})) if has_advisories else 0
                vuln_count = len(data.get("vulnerabilities", {})) if has_vulns else 0
                if adv_count == 0 and vuln_count == 0:
                    raise SystemExit(
                        f"::error::依赖审计报告统计与明细不一致：{path}\n"
                        f"metadata 声明存在 {meta_high} 个 high、{meta_critical} 个 critical 漏洞，"
                        "但 advisories 与 vulnerabilities 明细均为空字典。拒绝作为有效报告放行。"
                    )

    return data


def load_audit_json(path: Path) -> dict:
    data = load_json(path)
    return validate_audit_payload(data, path)


def high_critical_index(chk, data: dict, path: Path | str = "") -> dict[tuple[str, str], dict]:
    """返回 {(package, advisory): {"severity":..., "title":...}}，仅 high/critical。"""
    index: dict[tuple[str, str], dict] = {}
    for name, severity, advisory, title in chk.iter_vulns(data):
        sev = chk.normalize_severity(severity)
        if sev not in chk.HIGH_SEVERITIES or not name:
            continue
        key = (chk.normalize_package(name), chk.normalize_advisory(advisory))
        index[key] = {"severity": sev, "title": title or "", "package": name, "advisory": advisory}

    # 深度一致性校验：若 metadata 统计明确声明存在 high 或 critical，则解析出的 high/critical 索引不得为空
    meta_vulns = data.get("metadata", {}).get("vulnerabilities") if isinstance(data.get("metadata"), dict) else None
    if isinstance(meta_vulns, dict):
        meta_high = meta_vulns.get("high", 0) or 0
        meta_critical = meta_vulns.get("critical", 0) or 0
        if (meta_high + meta_critical) > 0 and len(index) == 0:
            target = f"：{path}" if path else ""
            raise SystemExit(
                f"::error::依赖审计报告解析与统计不一致{target}\n"
                f"metadata 统计存在 {meta_high} 个 high、{meta_critical} 个 critical 漏洞，"
                "但未能在明细中解析出任何有效的 high/critical 漏洞。拒绝作为有效报告放行。"
            )

    return index


def parse_date(value, field: str, errors: list[str]) -> date | None:
    if value is None:
        errors.append(f"{field} 缺失")
        return None
    try:
        return date.fromisoformat(str(value).strip())
    except ValueError:
        errors.append(f"{field} 不是合法 ISO8601 日期：{value!r}")
        return None


def check_exceptions(chk, prod: dict, exc_path: Path, today: date):
    """检查 A：例外清单有效性。返回 (errors, warnings, stats)。"""
    errors: list[str] = []
    warnings: list[str] = []
    entries = chk.parse_exceptions(str(exc_path))

    if not entries:
        errors.append(f"{exc_path} 中没有任何例外条目（文件损坏或为空？）")

    valid = 0
    for entry in entries:
        package = entry.get("package") or "<unknown>"
        advisory_raw = entry.get("advisory") or ""
        key = (chk.normalize_package(package), chk.normalize_advisory(advisory_raw))

        expires = parse_date(entry.get("expires_on"), f"{package} 的 expires_on", errors)

        owner = (entry.get("owner") or "").strip()
        if not owner:
            errors.append(f"例外缺少 owner：{package} [{advisory_raw}]")
        elif "your-domain" in owner or "example.com" in owner or owner.lower() in {"todo", "tbd", "n/a"}:
            errors.append(
                f"例外 owner 是占位符而非真实责任人：{package} [{advisory_raw}] owner={owner!r}"
            )

        for field in ("reason", "mitigation", "severity"):
            if not (entry.get(field) or "").strip():
                errors.append(f"例外缺少 {field}：{package} [{advisory_raw}]")

        matched = prod.get(key)
        if matched is None:
            errors.append(
                "例外条目已失效（INERT）："
                f"{package} [{advisory_raw}] 不在生产依赖的 high/critical 结果中。"
                "官方检查器不会校验它，它会一直伪装成有效例外。"
                "→ 若漏洞确已消失请删除该条目；若只存在于 devDependencies，"
                "请移入 .github/audit-baseline-devdeps.json 的 accepted 列表。"
            )
            continue

        # 只有匹配到真实漏洞的条目才谈得上「过期」
        if expires is not None and expires < today:
            errors.append(
                f"例外已过期：{package} [{advisory_raw}] 到期日 {expires.isoformat()}（今天 {today.isoformat()}）"
            )
        exc_sev = chk.normalize_severity(entry.get("severity"))
        if exc_sev and exc_sev != matched["severity"]:
            errors.append(
                f"例外 severity 与审计不一致：{package} [{advisory_raw}] "
                f"声明 {exc_sev}，实际 {matched['severity']}"
            )
        valid += 1

    return errors, warnings, {"total": len(entries), "valid": valid}


def check_devdeps(chk, prod: dict, full: dict, baseline_path: Path, today: date):
    """检查 B：devDependencies 棘轮。返回 (errors, warnings, stats)。"""
    errors: list[str] = []
    warnings: list[str] = []

    if not baseline_path.exists():
        errors.append(
            f"缺少 devDependencies 基线文件：{baseline_path}\n"
            "该文件用于记录已接受的 dev-only high/critical 集合，缺失时无法判定是否新增漏洞。"
        )
        return errors, warnings, {"dev_only": 0, "new": 0, "resolved": 0}

    baseline = load_json(baseline_path)
    accepted = {
        (chk.normalize_package(item.get("package", "")), chk.normalize_advisory(item.get("advisory", "")))
        for item in baseline.get("accepted", [])
    }

    recheck = parse_date(baseline.get("recheck_on"), f"{baseline_path} 的 recheck_on", errors)
    if recheck is not None and recheck < today:
        errors.append(
            f"devDependencies 基线已超过复核日期：{recheck.isoformat()}（今天 {today.isoformat()}）。"
            "请重新评估 dev-only 高危依赖并更新基线。"
        )

    dev_only = {k: v for k, v in full.items() if k not in prod}

    new_findings = sorted(k for k in dev_only if k not in accepted)
    resolved = sorted(k for k in accepted if k not in dev_only)

    if new_findings:
        errors.append("devDependencies 出现基线外的新 high/critical 漏洞：")
        for key in new_findings:
            info = dev_only[key]
            errors.append(
                f"  - {info['package']} [{info['advisory']}] ({info['severity']})"
                + (f"：{info['title']}" if info["title"] else "")
            )
        errors.append(
            "  → 升级/移除该依赖，或评估影响后加入 "
            f"{baseline_path} 的 accepted 列表并注明理由与复核日期。"
        )

    if resolved:
        warnings.append(
            "以下基线条目在当前审计中已不再出现，建议从基线移除（保持基线可信）："
        )
        for key in resolved:
            warnings.append(f"  - {key[0]} [{key[1]}]")

    return errors, warnings, {
        "dev_only": len(dev_only),
        "new": len(new_findings),
        "resolved": len(resolved),
        "accepted": len(accepted),
    }


def selftest() -> int:
    print("=== audit-gate.py 输出校验自检 ===")
    failures = 0

    # 1. 正常结构（包含 advisories）应成功
    ok_payload = {"advisories": {}, "metadata": {}}
    try:
        validate_audit_payload(ok_payload, "test-ok.json")
        print("  [OK] 正常 audit payload (含 advisories): pass")
    except SystemExit as e:
        print(f"  [FAIL] 正常 audit payload 误报失败: {e}")
        failures += 1

    # 2. 包含 error 字段（模拟扫描失败）必须 fail-closed
    err_payload = {"error": {"code": "ENOTFOUND", "summary": "registry down"}}
    try:
        validate_audit_payload(err_payload, "test-err.json")
        print("  [FAIL] error payload 未阻断")
        failures += 1
    except SystemExit:
        print("  [OK] error payload 正确阻断 (fail-closed)")

    # 3. 结构缺失（如空 dict 或无预期字段）必须 fail-closed
    empty_payload = {"other_key": 123}
    try:
        validate_audit_payload(empty_payload, "test-empty.json")
        print("  [FAIL] 缺少 audit 核心字段的 payload 未阻断")
        failures += 1
    except SystemExit:
        print("  [OK] 缺少 audit 核心字段正确阻断 (fail-closed)")

    # 4. 仅含 metadata 统计（10 high、1 critical）却缺失 advisories/vulnerabilities 明细字段 → 必须 fail-closed
    meta_only_payload = {"metadata": {"vulnerabilities": {"high": 10, "critical": 1}}}
    try:
        validate_audit_payload(meta_only_payload, "test-meta-only.json")
        print("  [FAIL] 仅含 metadata 统计却缺失明细字段未阻断")
        failures += 1
    except SystemExit:
        print("  [OK] 仅含 metadata 统计缺少明细正确阻断 (fail-closed)")

    # 5. 包含 metadata 统计（10 high、1 critical），但 advisories 与 vulnerabilities 为空字典 → 必须 fail-closed
    mismatch_empty_payload = {
        "advisories": {},
        "vulnerabilities": {},
        "metadata": {"vulnerabilities": {"high": 10, "critical": 1}},
    }
    try:
        validate_audit_payload(mismatch_empty_payload, "test-mismatch-empty.json")
        print("  [FAIL] 统计注明 high/critical 但明细为空字典未阻断")
        failures += 1
    except SystemExit:
        print("  [OK] 统计与明细不一致（统计有高危但明细为空）正确阻断 (fail-closed)")

    # 6. 字段类型异常（如 advisories 为 list 或 metadata 不是 dict）必须 fail-closed
    bad_type_payload = {"advisories": ["not-a-dict"], "metadata": {}}
    try:
        validate_audit_payload(bad_type_payload, "test-bad-type.json")
        print("  [FAIL] advisories 类型非 dict 未阻断")
        failures += 1
    except SystemExit:
        print("  [OK] advisories 类型非 dict 正确阻断 (fail-closed)")

    # 7. high_critical_index 深度一致性校验
    class FakeChecker:
        HIGH_SEVERITIES = {"high", "critical"}

        def iter_vulns(self, data):
            # 返回空迭代器，模拟未能解析出 high/critical 条目
            return []

        def normalize_severity(self, s):
            return str(s).lower()

        def normalize_package(self, p):
            return str(p).lower()

        def normalize_advisory(self, a):
            return str(a)

    try:
        high_critical_index(FakeChecker(), mismatch_empty_payload, "test-fake.json")
        print("  [FAIL] high_critical_index 深度一致性校验未阻断")
        failures += 1
    except SystemExit:
        print("  [OK] high_critical_index 深度一致性校验正确阻断 (fail-closed)")

    if failures:
        print(f"::error::audit-gate 自检失败：{failures} 项未通过")
        return 1
    print("自检通过（7/7）")
    return 0


def main() -> int:
    if "--selftest" in sys.argv:
        return selftest()

    parser = argparse.ArgumentParser(description="前端依赖审计门禁（例外有效性 + devDependencies 棘轮）")
    parser.add_argument("--prod", required=True, help="pnpm audit --prod --json 输出")
    parser.add_argument("--full", required=True, help="pnpm audit --json 全量输出（含 devDependencies）")
    parser.add_argument("--exceptions", required=True, help=".github/audit-exceptions.yml")
    parser.add_argument("--baseline", required=True, help="devDependencies 基线 JSON")
    parser.add_argument("--today", default=None, help="覆盖今天日期（ISO8601，仅用于测试）")
    parser.add_argument("--json-out", default=None, help="把结论写入 JSON 文件（供 Artifact 使用）")
    parser.add_argument("--selftest", action="store_true", help="自检输出校验逻辑")
    args = parser.parse_args()

    today = date.fromisoformat(args.today) if args.today else date.today()

    chk = load_official_checker()

    for label, raw in (("--prod", args.prod), ("--full", args.full)):
        if not Path(raw).exists():
            print(f"::error::{label} 文件不存在：{raw}", file=sys.stderr)
            return 2

    prod = high_critical_index(chk, load_audit_json(Path(args.prod)))
    full = high_critical_index(chk, load_audit_json(Path(args.full)))

    print("=== 前端依赖审计门禁 ===")
    print(f"prod high/critical 条目：{len(prod)}")
    print(f"full high/critical 条目：{len(full)}")
    print(f"dev-only high/critical：{len(full) - len(prod) if len(full) >= len(prod) else '?'}")
    print()

    exc_errors, exc_warnings, exc_stats = check_exceptions(chk, prod, Path(args.exceptions), today)
    dev_errors, dev_warnings, dev_stats = check_devdeps(
        chk, prod, full, Path(args.baseline), today
    )

    print("--- A. 例外清单有效性（对照 prod 审计）---")
    print(f"  条目 {exc_stats['total']} 条，其中有效 {exc_stats['valid']} 条")
    print("--- B. devDependencies 棘轮 ---")
    print(
        f"  dev-only {dev_stats['dev_only']} 条 / 基线接受 {dev_stats.get('accepted', 0)} 条"
        f" / 新增 {dev_stats['new']} 条 / 已解决 {dev_stats['resolved']} 条"
    )
    print()

    warnings = exc_warnings + dev_warnings
    for line in warnings:
        print(f"::warning::{line}")
    if warnings:
        print()

    errors = exc_errors + dev_errors
    if errors:
        for line in errors:
            for sub in str(line).splitlines():
                print(f"::error::{sub}")
        print()
        print("审计门禁未通过。")
        conclusion = "fail"
    else:
        print("审计门禁通过。")
        conclusion = "pass"

    if args.json_out:
        Path(args.json_out).write_text(
            json.dumps(
                {
                    "schema_version": "1",
                    "generated_at": datetime.now().isoformat(timespec="seconds"),
                    "today": today.isoformat(),
                    "conclusion": conclusion,
                    "prod_high_critical": len(prod),
                    "full_high_critical": len(full),
                    "exceptions": exc_stats,
                    "devdeps": dev_stats,
                    "errors": [str(e) for e in errors],
                    "warnings": [str(w) for w in warnings],
                },
                ensure_ascii=False,
                indent=2,
            )
            + "\n",
            encoding="utf-8",
        )

    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
