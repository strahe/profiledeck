# 数据与安全

ProfileDeck 管理本地文件和本地 secret。请把 runtime 目录按敏感数据处理。

## Runtime 数据

runtime root 是：

```text
<os-user-config-dir>/profiledeck
```

其中包含：

- `profiledeck.db`
- `backups/`
- `exports/`
- `logs/`
- `locks/`

`--config-dir` 会改变用于解析 runtime root 的用户配置目录。

## SQLite 数据库

`profiledeck.db` 保存 ProfileDeck 应用数据。对 Codex 来说，它会在隐藏 credential 记录中保存 raw `auth.json` payload，并在 Config Set 中保存完整 `config.toml` payload。这里是资源的长期状态；`$CODEX_HOME` 中的文件是 active Profile 的工作副本。

每个 Config Set 限制为 16 MiB，并校验 SHA-256 hash。即使数据库列名不表示 secret，payload 仍可能包含 token 或其他敏感配置。

数据库不会做静态加密。在 POSIX 系统上，ProfileDeck 会尽力收紧文件权限，但本地文件系统访问权限仍然是安全边界。

## 备份

switch 和 rollback 备份可能包含之前的目标文件内容。对 Codex 来说，这可能包括 raw `auth.json` 和 `config.toml`。

backup 命令展示 path、action、hash 和 mode 等 metadata，不会在常规输出中打印 raw auth content。

## 脱敏

ProfileDeck 会在 preview 和命令输出中脱敏看起来敏感的值。Codex auth preview 始终整体脱敏。Config Set 与 Profile API 只暴露 metadata 摘要，不输出 raw TOML 或 auth payload。

以下命令只输出 metadata，不打印 raw auth：

```bash
profiledeck codex profile list
profiledeck codex profile show <profile-id>
profiledeck codex config-set list
profiledeck codex config-set show <config-set-id>
profiledeck plan codex <profile-id>
profiledeck backup show <backup-id>
profiledeck doctor
```

## ProfileDeck 不保存什么

ProfileDeck usage import 保存派生的 token 和 cost 记录。它不会把 raw Codex JSONL events、prompts、completions 或 API keys 作为 usage metadata 持久化。

Config Set v1 不捕获 skills、plugin 安装缓存、项目 `.codex/config.toml` 或系统策略。
