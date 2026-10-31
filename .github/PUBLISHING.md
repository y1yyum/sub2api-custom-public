# 公开仓库发行衔接

更新日期：2026-10-31。关联 [Issue #21](https://github.com/y1yyum/sub2api-custom-public/issues/21)。

## 执行路径

当前基线使用 `local-ci.yml` 的 Gitea 兼容策略：PR 按改动范围快速检查，
版本标签和手动运行执行当前公开快照的全量检查。两种范围不同，PR 的绿色
`ci-gate` 不能直接作为完整发行证明。“全量”不表示恢复已裁剪的上游测试闭包。

正常发行只进行一次标签全量 CI：

```text
PR 快检 → 受保护合并 → 创建版本标签
  → Local CI 全量完成
  → Publish 校验同一提交的全量收据
  → 刷新漏洞数据 → 构建实际镜像并冒烟
  → 签名、来源证明、SBOM、离线包 → 版本镜像标签和 GitHub Release
```

`publish.yml` 监听 `Local CI` 的完成事件，不直接监听 tag push，也不调用、
调度或重跑 CI。main push 继续不重复执行验证。手动补发同样要求已有全量证据；
证据缺失、过期或失败会阻断，并要求先完成对应提交的 Local CI，不隐式启动长测试。

## 全量证据与权限边界

GitHub 的 `ci-gate` 在原有门禁成功后，额外保存源码 SHA、Git 树、事件、
范围输出和八个依赖作业结果，上传 `local-ci-evidence-<attempt>`。
这三个 GitHub 元数据传输步骤与工具准备一样属于平台接入；Gitea 的选测规则和
原有检查命令保持一致。release-helper 测试发现范围在两份工作流中同步扩展，以运行新门禁自检。

Publisher 在授予镜像写入权限前验证：

- 标签为 SemVer、VERSION 匹配，且源码提交是 main 的祖先。
- 来源属于本仓库的 `Local CI`，事件为 push 或手动运行，不能是 PR。
- 使用同一 SHA 的最新运行，不能以旧绿色结果覆盖新失败或正在运行的结果。
- 九项真实 Actions 作业全部成功；部分重跑只取每项作业的最新执行，最终门禁属于当前 attempt。
- Artifact 属于同一 Run/attempt，未过期，SHA256 正确，仅包含一个有界 JSON 文件。
- 收据证明同一提交/树、完整范围和全部依赖成功。任何跳过、选测或源码差异都拒绝发行。

读取的是 Actions 作业和不可变 Artifact，不依赖可写的同名 Commit Status。
Artifact 只在内存中解析，不解压到脚本或构建目录。发布控制脚本来自可信工作流版本；
应用源码在校验通过后按已解析的 SHA 检出。自动入口也先过滤 PR、异库和非版本 ref。

[`workflow_run` 使用默认分支的控制器身份，且后续工作流可能具有更高权限](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_run)。
因此应用源码 SHA 与发布工作流 SHA 分别记录，不把二者混写。

## 安全检查与交付

全量 Local CI 的安全、lint、依赖审计、秘密扫描和部署约束仍是前置条件。
Publisher 再刷新 govulncheck 和 npm 审计，并应用现有例外/到期/开发依赖基线规则。
实际发行镜像按 Digest 冒烟，通过后生成签名和来源证明；版本镜像标签在这些步骤后更新。

`release-ci-evidence.json` 记录实际源码、CI、覆盖范围和控制器身份，
并以独立 Cosign attestation 绑定镜像 Digest。GitHub 构建来源证明标识控制器；
应用源码与 CI 的对应关系见独立来源证明。验证命令随 Release 提供。

已存在 GitHub Release 的版本直接返回，保留既有镜像标签、签名和附件。
相同标签的发布并发串行化；Pre-release 不更新 `X.Y` 或 `latest`。
应用侧不连接生产数据库，不执行 SSH 部署；迁移和支付实机条件仍需部署侧独立验收。

## 验证

```bash
python3 -m pip install -r .github/release-tools/requirements-release.txt
python3 -m unittest discover -s .github/release-tools -p 'test_*.py' -v
actionlint .github/workflows/publish.yml
```

真实验证分别检查 PR 的 scoped 收据和手动全量运行的 full 收据，
并以已发行标签验证幂等返回。测试不移动标签、不创建额外版本、不重建已发行镜像。

2026-10-31 的 `v0.4.0-rc.1` 已通过受限应急发行分支完成；源码、镜像 Digest、
签名和实际验证证据见 [Issue #19](https://github.com/y1yyum/sub2api-custom-public/issues/19)。
该版本保持原样。旧隔离执行器的性能调查保留为历史记录，不作为当前裁剪快照 CI 的性能比较。
