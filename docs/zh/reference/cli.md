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
| `usage` | 导入并汇总本地 token 用量。 |
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
profiledeck codex profile create <profile-id> [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex profile fork <source-profile-id> <new-profile-id> --auth-binding share-parent|copy-new [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex profile sync <profile-id> [--auth-update update-shared|fork-new] [--codex-dir PATH] [--json]
```

`create` 从当前 Codex `config.toml` 和 `auth.json` 创建 profile，并创建新的隐藏 credential。`fork` 复制已有 profile，且必须显式选择 auth 绑定方式。`sync` 从当前 Codex 文件更新已有 profile；当更新共享 credential 或刻意分叉 credential 时使用 `--auth-update`。

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
```

当前只支持 Codex 本地 session 用量。

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

## 备份与恢复

```bash
profiledeck backup list [--json]
profiledeck backup show <backup-id> [--json]
profiledeck doctor [--json]
profiledeck doctor repair-lock --yes [--json]
profiledeck recover <switch-operation-id> --yes [--json]
profiledeck rollback <backup-id> --yes [--json]
```
