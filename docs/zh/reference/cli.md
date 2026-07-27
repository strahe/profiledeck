# CLI 参考

本页用于查找命令名称和常用选项。请运行 `profiledeck-cli --help` 或 `profiledeck-cli <command> --help`，查看当前安装版本包含的准确帮助；如果与本页不同，以安装版本的帮助为准。

尖括号表示必填值，方括号表示可选参数。

## 全局选项

所有命令都支持：

```text
--config-dir string  Use a custom ProfileDeck config directory
```

该值是用户配置根目录。ProfileDeck 会在其下创建或使用 `profiledeck` 文件夹。

## 命令

| 命令 | 用途 |
| --- | --- |
| `antigravity` | 保存和管理 Antigravity Profile。 |
| `backup` | 创建、导出、恢复和管理加密应用备份。 |
| `claude-code` | 保存和管理 Claude Code 账号登录 Profile。 |
| `codex` | 管理 Codex Profile 和已保存设置（配置集）。 |
| `doctor` | 诊断本地数据、权限和中断操作的问题。 |
| `init` | 创建 ProfileDeck 本地数据。 |
| `provider` | 为其他 AI 工具配置高级文件切换。 |
| `profile` | 管理 Profile 和高级文件目标。 |
| `recover` | 处理被中断或失败的切换。 |
| `status` | 检查 ProfileDeck 是否已初始化。 |
| `switch` | 预览或应用 Profile 切换。 |
| `usage` | 导入和报告本地 Codex 用量。 |
| `version` | 输出版本信息。 |

## 初始化与状态

```bash
profiledeck-cli init [--json]
profiledeck-cli status [--json]
profiledeck-cli version
```

## Codex

```bash
profiledeck-cli codex detect [--codex-dir PATH] [--json]
profiledeck-cli codex profile list [--json]
profiledeck-cli codex profile show <profile-id> [--json]
profiledeck-cli codex profile create <profile-id> [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex profile fork <source-profile-id> <new-profile-id> --credential-binding share-parent|copy-new --config-binding share-parent|copy-new [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex profile save-current [--codex-dir PATH] [--json]
profiledeck-cli codex profile set-config <profile-id> <config-set-id> [--json]
profiledeck-cli codex profile delete <profile-id> --yes [--json]
profiledeck-cli codex profile export [<profile-id> ...] --output PATH [--force] [--json]
profiledeck-cli codex profile import inspect <bundle-path> [--codex-dir PATH] [--json]
profiledeck-cli codex profile import apply <bundle-path> --plan-fingerprint FINGERPRINT --yes [--codex-dir PATH] [--json]

profiledeck-cli codex config-set list [--json]
profiledeck-cli codex config-set show <config-set-id> [--json]
profiledeck-cli codex config-set create <config-set-id> [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex config-set copy <source-id> <new-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex config-set update <config-set-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex config-set delete <config-set-id> --yes [--json]
```

第一次运行 `profile create` 会保存当前 Codex 登录和设置，并创建 `shared` 配置集。后续创建默认复用当前配置集，除非传入 `--new-config-set`。

`fork` 要求同时选择登录和配置集的处理方式，且至少一项必须是 `copy-new`。复制设置时还必须提供 `--new-config-set`。`save-current` 保存 Codex 当前使用的登录和设置；`set-config` 只能更改非当前 Profile。

`config-set create` 保存当前 `config.toml`。列表和详情命令只返回安全摘要。只有未被任何 Profile 使用的配置集才能删除。

`profile export` 会创建敏感备份。不指定 Profile ID 时，它会导出全部 Codex Profile 和配置集；指定 ID 时，只导出所选 Profile 及其所需数据。覆盖已有输出文件时需要 `--force`。

请先运行 `import inspect`，检查哪些内容会新增、哪些与已保存数据一致，以及是否有冲突，再把返回的指纹传给 `import apply`。已有 Codex 数据冲突时，导入会停止且不做更改。导入不会把 Profile 设为当前 Profile，也不会更改 Codex 文件。

任务示例与安全说明见 [Codex Profile](../codex/profiles.md)。

## Claude Code

```bash
profiledeck-cli claude-code detect [--json]
profiledeck-cli claude-code profile create <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli claude-code profile list [--json]
profiledeck-cli claude-code profile show <profile-id> [--json]
profiledeck-cli claude-code profile update <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli claude-code profile save-current [--yes] [--json]
profiledeck-cli claude-code profile delete <profile-id> --yes [--json]
```

`create` 保存当前 Claude Code 账号登录，并把新 Profile 设为当前 Profile。`save-current` 更新当前 Profile 使用的登录。如果该登录被共享，命令会报告受影响的 Profile 数量，并要求传入 `--yes`。

Claude Code 没有 `claude` 别名。如需预览，请运行 `profiledeck-cli switch claude-code <profile-id> --dry-run`；应用时运行 `profiledeck-cli switch claude-code <profile-id> --yes`。命令只显示登录状态和安全元数据，不会显示令牌值。

登录要求与验证方式见 [Claude Code Profile](../claude-code/profiles.md)。

## Antigravity

```bash
profiledeck-cli antigravity detect [--json]
profiledeck-cli antigravity profile list [--json]
profiledeck-cli antigravity profile show <profile-id> [--json]
profiledeck-cli antigravity profile create <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli antigravity profile update <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli antigravity profile save-current [--json]
profiledeck-cli antigravity profile delete <profile-id> --yes [--json]
```

`agy` 是 `antigravity` 的别名。`create` 和 `save-current` 要求 Antigravity 当前存在有效的个人 OAuth 登录。输出只显示安全元数据，不会打印登录内容。

兼容性和切换建议见 [Antigravity Profile](../antigravity/profiles.md)。

## 预览与切换

```bash
profiledeck-cli switch --dry-run [--json] <provider-id> <profile-id>
profiledeck-cli switch --yes [--plan-fingerprint FINGERPRINT] [--json] <provider-id> <profile-id>
```

`switch --dry-run` 是可选的只读预览。使用 `switch --yes` 应用变更。如果希望 ProfileDeck 在检查后状态发生变化时拒绝切换，请传入预览返回的指纹。

## 用量

```bash
profiledeck-cli usage sync codex [--codex-dir PATH] [--json]
profiledeck-cli usage summary [--provider codex] [--json]
profiledeck-cli usage report [--provider codex] [--range today|7d|30d|all] [--json]
```

目前只支持本地 Codex 用量。`report` 默认范围为 `7d`；`summary` 提供更简短的全量视图。报告字段和估算限制见 [Codex 用量与成本](../codex/usage-cost.md)。

## 其他工具与配置文件

以下命令是面向其他工具的高级 CLI 功能。Codex、Claude Code 和 Antigravity 必须使用上方各自的专用命令；通用文件目标命令不能管理它们保存的登录或设置。

```bash
profiledeck-cli provider list [--json]
profiledeck-cli provider show <id> [--json]
profiledeck-cli provider create <id> [--name NAME] [--adapter ID] [--metadata-json JSON] [--json]
profiledeck-cli provider update <id> [--name NAME] [--adapter ID] [--metadata-json JSON] [--json]
profiledeck-cli provider delete <id> --yes [--json]

profiledeck-cli profile list [--json]
profiledeck-cli profile show <id> [--json]
profiledeck-cli profile create <id> [--name NAME] [--description TEXT] [--metadata-json JSON] [--json]
profiledeck-cli profile update <id> [--name NAME] [--description TEXT] [--metadata-json JSON] [--json]
profiledeck-cli profile delete <id> --yes [--json]
```

删除 Provider 会清除全部由它拥有的 ProfileDeck 数据，包括设置、已保存资源及绑定、文件目标、当前 Profile 状态、用量报告和已完成操作记录。全局 Profile、桌面端 Agent 偏好，以及工具当前使用的登录、设置和文件会保留。Provider 存在未完成操作时，删除会停止。

以上四种 Profile 删除命令执行同一个全局删除。即使 Profile 只包含其他 Agent 的数据，从某个 Agent 命令进入也会删除整个 Profile。Profile 是任一 Agent 的当前 Profile，或存在未完成操作时，删除会停止。只有该 Profile 使用的已保存登录和配置集会一并删除；共享数据和无关的未绑定数据会保留。引用该 Profile 的已完成操作记录也会删除。工具当前使用的登录、设置和文件不会改变。

文件目标命令：

```bash
profiledeck-cli profile target add <profile-id> <target-id> --provider ID --path PATH --format FORMAT --strategy STRATEGY --value-json JSON [--disabled] [--metadata-json JSON] [--json]
profiledeck-cli profile target list <profile-id> [--provider ID] [--all] [--json]
profiledeck-cli profile target show <profile-id> <provider-id> <target-id> [--json]
profiledeck-cli profile target update <profile-id> <provider-id> <target-id> [--path PATH] [--format FORMAT] [--strategy STRATEGY] [--value-json JSON] [--enabled] [--disabled] [--metadata-json JSON] [--json]
profiledeck-cli profile target delete <profile-id> <provider-id> <target-id> --yes [--json]
```

添加文件目标前，请先阅读[其他配置文件](../guide/generic-targets.md)。

## 备份、诊断与恢复

```bash
profiledeck-cli backup create [--json]
profiledeck-cli backup list [--json]
profiledeck-cli backup show <backup-id> [--json]
profiledeck-cli backup export <backup-id> --output <file> [--json]
profiledeck-cli backup restore [<backup-id> | --file <file>] --yes [--json]
profiledeck-cli backup delete <backup-id> --yes
profiledeck-cli backup key status [--json]
profiledeck-cli backup key export --output <file> --yes [--json]
profiledeck-cli backup key import --file <file> [--replace] --yes [--json]
profiledeck-cli doctor [--json]
profiledeck-cli doctor repair-lock --yes [--json]
profiledeck-cli recover <operation-id> --yes [--json]
```

应用备份包含完整的 ProfileDeck 数据库，但不包含工具自己的工作文件或系统凭据存储条目。把备份移到其他系统前，请单独导出恢复密钥。替换不同密钥时必须同时传入 `--replace` 和 `--yes`；当前密钥将无法打开旧密钥加密的备份。

`recover` 只处理“诊断”报告的未完成切换。成功切换不能撤销。不同状态下的安全操作见[诊断与恢复](../operations/recovery.md)。
