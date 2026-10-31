# 秘密扫描误报复核

日期：2026-10-31。关联 [Issue #21](https://github.com/y1yyum/sub2api-custom-public/issues/21)。

## 三个历史非秘密指纹

手动全量运行 41229235974 在扫描全部公开历史时发现三项 generic-api-key 命中。
使用与 CI 相同的 Gitleaks 8.30.1 和命令，在独立完整公开克隆中复现为三项；
没有扫描包含开发仓库私有历史的共享对象库，也没有输出或提交原始匹配。

三项均来自不可变公开提交 `0cffc85f33c2831a25f0f7f29357640e26f3dc91`：

- `backend/internal/repository/ops_repo_preagg.go` 第 208、321 行：SQL UPSERT 的列到列赋值，
  被规则中的词形匹配误识别为认证材料。两行内容相同，逐行摘要均为
  `6ccba78847e6ce971d6f2fec92a285313a2db89fcbab312cc5cc9b96ed9b583e`。
- `backend/internal/service/plugin_package.go` 第 28 行：内置发布方的 Ed25519 公共验证值。
  解码为 32 字节，由该文件第 278、282 行按 `ed25519.PublicKeySize` 和
  `ed25519.Verify` 校验使用；不是签名私钥。解码值的 SHA256 为
  `fb126f7384b90fe4359f990c43d18d510f9bb79216ea0a956d996d7e8c40d976`，
  该行摘要为 `75e58533ff2f315f1d079d63c798134db308deab8c411a836d4442977c616bef`。

不修改这些应用代码，不改写已发布历史，也不放宽扫描规则或整文件路径。
`.github/secret-scan-exceptions.json` 仅登记上述三个完整的
Commit/File/Rule/Line 指纹；其他提交、其他行或其他规则不匹配。

## 到期与审计

- 责任人：`y1yyum`；到期日：2026-11-30（UTC，当日开始失效）。
- 常规扫描仍完整执行，再分离已复核位置；报告保留位置、原因、责任人和到期信息。
- 例外缺少责任人、重复、含通配或过期时扫描器阻断，不能静默沿用。
- strict 模式不读取或应用这些例外，仍保留全部命中供公开放行复核。

这份复核仅证明三个特定位置是非秘密数据，不证明整个仓库没有泄露，
也不替代其他秘密扫描、真实凭据撤销或安全发布审查。
