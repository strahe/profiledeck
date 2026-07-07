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

`profiledeck.db` 保存 ProfileDeck 应用数据。对 Codex 来说，它还会在专用 account secret 表中保存 raw `auth.json` payload，以便在 switch 时恢复账号。

数据库不会做静态加密。在 POSIX 系统上，ProfileDeck 会尽力收紧文件权限，但本地文件系统访问权限仍然是安全边界。

## 备份

switch 和 rollback 备份可能包含之前的目标文件内容。对 Codex 来说，这可能包括 raw `auth.json`。

backup 命令展示 path、action、hash 和 mode 等 metadata，不会在常规输出中打印 raw auth content。

## 脱敏

ProfileDeck 会在 preview 和命令输出中脱敏看起来敏感的值。Codex auth preview 始终整体脱敏。

以下命令只输出 metadata，不打印 raw auth：

```bash
profiledeck codex account list
profiledeck codex account show <account-id>
profiledeck plan codex <profile-id>
profiledeck backup show <backup-id>
profiledeck doctor
```

## 显式 auth export

这个命令会有意写出 raw auth JSON：

```bash
profiledeck codex account export <account-id> --output ./auth.json
```

导出的文件是 secret。查看、编辑、移动和删除它时，应采用与 Codex 原始 `auth.json` 相同的处理标准。

## ProfileDeck 不保存什么

ProfileDeck usage import 保存派生的 token 和 cost 记录。它不会把 raw Codex JSONL events、prompts、completions 或 API keys 作为 usage metadata 持久化。
