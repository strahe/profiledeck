# CLI 参考

所有命令都支持全局选项：

```text
--config-dir string  使用自定义的 ProfileDeck 配置目录
```

## 根命令

| 命令 | 用途 |
| --- | --- |
| `antigravity` | 管理 Antigravity agy v2 Profile。 |
| `backup` | 查看 ProfileDeck 备份。 |
| `claude-code` | 管理 Claude Code 官方订阅 Profile。 |
| `codex` | 管理 Codex provider 的 profile。 |
| `doctor` | 检查本地数据、文件权限和未完成的更改。 |
| `init` | 创建 ProfileDeck 本地数据。 |
| `plan` | 预览 Profile 切换，不修改外部目标。 |
| `provider` | 管理 AI 工具 provider。 |
| `profile` | 管理 ProfileDeck profile 和 target。 |
| `recover` | 使用失败切换前保存的备份恢复外部目标。 |
| `rollback` | 使用备份撤销已完成的切换。 |
| `status` | 查看 ProfileDeck 初始化状态。 |
| `switch` | 应用 profile 切换。 |
| `usage` | 导入并分析本地 token 用量。 |
| `version` | 打印版本信息。 |

## 初始化与状态

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

第一次 `profile create` 会保存当前 Codex 登录和设置，并创建 `shared` Config Set。后续创建默认复用当前 Config Set，除非传入 `--new-config-set`。`fork` 必须指定两个共享选项，且至少一项为 `copy-new`；复制设置时还必须提供 `--new-config-set`。`save-current` 保存当前 Codex 登录和设置；`set-config` 只接受非当前 Profile。

`config-set create` 保存当前 `config.toml`。List 和 show 只返回摘要，不暴露完整登录数据或 TOML。只有未被任何 Profile 使用的 Config Set 才能删除。

`profile export` 会创建敏感备份。不指定 Profile ID 时，它会导出全部 Codex Profiles 和 Config Sets。指定 Profile ID 时，只导出这些 Profiles 及其需要的登录和 Config Sets。当前 Codex 登录或设置有变化时，请先运行 `save-current`。`--output` 必填，便于把备份放到即将删除的 ProfileDeck 数据目录之外；覆盖已有文件必须传入 `--force`。

备份包含完整的 Codex 登录数据和设置。ProfileDeck 会在 POSIX 系统上以 `0600` 权限写入文件；stdout 不会打印敏感内容。`import inspect` 会检查备份并报告 `create`、`unchanged` 和 `conflict`。`import apply` 必须提供审核过的 fingerprint；已有 Codex 数据冲突时，不会写入任何更改。已有全局 Profile 尚无 Codex 绑定时，导入会附加绑定，但不会更改其名称或描述。导入不会把 Profile 设为当前，也不会写入 Codex 文件。

## Claude Code

```bash
profiledeck claude-code detect [--json]
profiledeck claude-code profile create <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck claude-code profile list [--json]
profiledeck claude-code profile show <profile-id> [--json]
profiledeck claude-code profile update <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck claude-code profile save-current [--yes] [--json]
```

Create 保存当前 Claude Code 官方订阅登录，并把新 Profile 设为当前。`save-current` 更新 active Profile 绑定的登录；隐藏登录被多个 Profile 共用时，命令会报告受影响 Profile 数量，并要求传入 `--yes`。

Claude Code 不提供更短的 `claude` alias。请使用 `profiledeck plan claude-code <profile-id>` 和 `profiledeck switch claude-code <profile-id> --yes` 切换。命令只显示登录状态和元数据，不显示 Token 值。

## Antigravity agy v2

```bash
profiledeck antigravity detect [--json]
profiledeck antigravity profile list [--json]
profiledeck antigravity profile show <profile-id> [--json]
profiledeck antigravity profile create <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck antigravity profile update <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck antigravity profile save-current [--json]
```

`agy` 是 `antigravity` 的别名。Create 和 save-current 要求 Antigravity agy v2 当前存在有效的 consumer OAuth 登录。输出只有元数据，不会打印登录内容。

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

当前只支持本地 Codex 用量。`sync codex` 是纯 CLI 场景的手动入口；Desktop 会按设置的间隔自动同步。`report` 默认使用 `7d`；人类可读输出显示总体摘要、时间趋势和模型统计。JSON 输出还包含本地时间范围、导入状态和定价来源。`summary` 提供更精简的全量视图。

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

对于受管的 `codex`、`claude-code` 和 `antigravity` Provider，通用 Provider 更新只能更改显示名称或启用状态；adapter 和 metadata 由各自的专用 Profile 流程管理。

target 命令：

```bash
profiledeck profile target add <profile-id> <target-id> --provider ID --path PATH --format FORMAT --strategy STRATEGY --value-json JSON [--disabled] [--metadata-json JSON] [--json]
profiledeck profile target list <profile-id> [--provider ID] [--all] [--json]
profiledeck profile target show <profile-id> <provider-id> <target-id> [--json]
profiledeck profile target update <profile-id> <provider-id> <target-id> [--path PATH] [--format FORMAT] [--strategy STRATEGY] [--value-json JSON] [--enabled] [--disabled] [--metadata-json JSON] [--json]
profiledeck profile target delete <profile-id> <provider-id> <target-id> --yes [--json]
```

Generic target 命令不能创建、修改或删除 Codex、Claude Code 或 Antigravity Profile 管理的绑定。请使用上面的 Provider 专用命令。

## 备份与恢复

```bash
profiledeck backup list [--json]
profiledeck backup show <backup-id> [--json]
profiledeck doctor [--json]
profiledeck doctor repair-lock --yes [--json]
profiledeck recover <switch-operation-id> --yes [--json]
profiledeck rollback <backup-id> --yes [--json]
```
