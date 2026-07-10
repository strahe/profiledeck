# CLI 参考

所有命令都支持全局选项：

```text
--config-dir string  使用自定义的 ProfileDeck 配置目录
```

## 根命令

| 命令 | 用途 |
| --- | --- |
| `backup` | 查看 ProfileDeck 备份。 |
| `codex` | 管理 Codex provider 的 profile。 |
| `doctor` | 诊断 ProfileDeck 运行状态。 |
| `init` | 初始化应用数据库。 |
| `plan` | 构建只读切换计划。 |
| `provider` | 管理 AI 工具 provider。 |
| `profile` | 管理 ProfileDeck profile 和 target。 |
| `recover` | 从备份检查点恢复失败的 switch。 |
| `rollback` | 回滚已应用的 switch 备份。 |
| `status` | 打印应用数据库状态。 |
| `switch` | 应用 profile 切换。 |
| `usage` | 导入并分析本地 token 用量。 |
| `version` | 打印版本信息。 |

## Runtime 基础命令

```bash
profiledeck init [--json]
profiledeck status [--json]
profiledeck version
```

## Codex

```bash
profiledeck codex detect [--codex-dir PATH] [--json]
profiledeck codex profile list [--json]
profiledeck codex profile show <profile-id> [--json]
profiledeck codex profile create <profile-id> [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex profile fork <source-profile-id> <new-profile-id> --credential-binding share-parent|copy-new --config-binding share-parent|copy-new [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex profile save-current [--codex-dir PATH] [--json]
profiledeck codex profile set-config <profile-id> <config-set-id> [--json]
profiledeck codex profile export [<profile-id> ...] --output PATH [--force] [--json]
profiledeck codex profile import inspect <bundle-path> [--codex-dir PATH] [--json]
profiledeck codex profile import apply <bundle-path> --plan-fingerprint FINGERPRINT --yes [--codex-dir PATH] [--json]

profiledeck codex config-set list [--json]
profiledeck codex config-set show <config-set-id> [--json]
profiledeck codex config-set create <config-set-id> [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex config-set copy <source-id> <new-id> [--name NAME] [--description TEXT] [--json]
profiledeck codex config-set update <config-set-id> [--name NAME] [--description TEXT] [--json]
profiledeck codex config-set delete <config-set-id> --yes [--json]
```

第一次 `profile create` 会把当前 Codex 文件捕获为隐藏 credential 和 `shared` Config Set。后续创建默认复用 active Config Set，除非传入 `--new-config-set`。`fork` 必须指定两个绑定选项，且至少一项为 `copy-new`；复制配置时还必须提供 `--new-config-set`。`save-current` 捕获两个 active 工作副本；`set-config` 只接受 inactive Profile。

`config-set create` 捕获当前 `config.toml`。List 和 show 只返回摘要，不暴露 raw auth 或 raw TOML。只有未被引用的 Config Set 才能删除。

`profile export` 是显式敏感备份。不指定 Profile ID 时，它会导出全部 Codex Profiles、所有被引用的隐藏 credential，以及包括未绑定配置集在内的全部 Config Sets。指定 Profile ID 时，只导出这些 Profiles 及其依赖闭包。如果 active 工作副本有变化，请先运行 `save-current`。`--output` 必填，便于把 bundle 放到即将删除的 runtime 目录之外；覆盖已有文件必须传入 `--force`。

bundle 包含 raw `auth.json` 和完整 `config.toml` payload。ProfileDeck 会原子写入文件，并在 POSIX 系统上设置 `0600` 权限；stdout 不会打印这些 payload。`import inspect` 会校验 bundle 并报告 `create`、`unchanged` 和 `conflict`。`import apply` 必须提供审核过的 fingerprint；只要同 ID 内容不同，就会拒绝整次导入，不产生部分写入。导入只恢复数据库资源，不设置 active 状态，也不写 Codex 工作文件。

## 切换

```bash
profiledeck plan [--json] <provider-id> <profile-id>
profiledeck switch [--yes] [--plan-fingerprint FINGERPRINT] [--json] <provider-id> <profile-id>
```

`switch` 必须传入 `--yes`。

## 用量

```bash
profiledeck usage sync codex [--codex-dir PATH] [--json]
profiledeck usage summary [--provider codex] [--json]
profiledeck usage report [--provider codex] [--range today|7d|30d|all] [--json]
```

当前只支持 Codex 本地 session 用量。`sync codex` 继续作为纯 CLI 场景的手动入口；Desktop 会按设置的间隔自动同步。`report` 默认使用 `7d`；人类可读输出依次显示总体摘要、时间趋势和模型统计。JSON 输出还包含解析后的本地时间范围、导入健康状态和静态定价来源。原有 `summary` 输出继续作为轻量全量契约。

## Provider 与 profile CRUD

```bash
profiledeck provider list [--all] [--json]
profiledeck provider show <id> [--json]
profiledeck provider create <id> [--name NAME] [--adapter ID] [--disabled] [--metadata-json JSON] [--json]
profiledeck provider update <id> [--name NAME] [--adapter ID] [--enabled] [--disabled] [--metadata-json JSON] [--json]
profiledeck provider delete <id> --yes [--json]

profiledeck profile list [--json]
profiledeck profile show <id> [--json]
profiledeck profile create <id> [--name NAME] [--description TEXT] [--metadata-json JSON] [--json]
profiledeck profile update <id> [--name NAME] [--description TEXT] [--metadata-json JSON] [--json]
profiledeck profile delete <id> --yes [--json]
```

target 命令：

```bash
profiledeck profile target add <profile-id> <target-id> --provider ID --path PATH --format FORMAT --strategy STRATEGY --value-json JSON [--disabled] [--metadata-json JSON] [--json]
profiledeck profile target list <profile-id> [--provider ID] [--all] [--json]
profiledeck profile target show <profile-id> <provider-id> <target-id> [--json]
profiledeck profile target update <profile-id> <provider-id> <target-id> [--path PATH] [--format FORMAT] [--strategy STRATEGY] [--value-json JSON] [--enabled] [--disabled] [--metadata-json JSON] [--json]
profiledeck profile target delete <profile-id> <provider-id> <target-id> --yes [--json]
```

Generic target CRUD 不能创建、修改或删除 Codex preset target。Credential 与 Config Set 绑定必须使用上面的 Codex 命令。

## 备份与恢复

```bash
profiledeck backup list [--json]
profiledeck backup show <backup-id> [--json]
profiledeck doctor [--json]
profiledeck doctor repair-lock --yes [--json]
profiledeck recover <switch-operation-id> --yes [--json]
profiledeck rollback <backup-id> --yes [--json]
```
