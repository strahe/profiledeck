# 核心概念

ProfileDeck 将应用状态和目标工具文件分开管理。

## 应用数据库

`profiledeck.db` 是 ProfileDeck 自有数据的 SQLite source of truth：

- 服务商 (providers)
- 配置集 (profiles)
- 当前激活状态 (active state)
- 配置目标 (profile targets)
- 切换与回滚操作记录 (switch/rollback records)
- Codex 账号密钥 (account secrets)
- 已导入的用量事件 (usage events) 和游标 (cursors)

目标工具文件仍归对应工具所有。ProfileDeck 只会通过 switch 和 rollback 流程写入这些文件。

## Provider

provider 表示一个 AI 工具集成。当前实现的 adapter 是：

- `codex`：用于 Codex preset profile。
- `generic`：用于手动配置的目标文件。

provider 可以启用或禁用。

## Profile

profile 是一个命名的期望状态。一个 profile 可以包含一个或多个 provider target。对 Codex 来说，常见 profile id 是 `work` 或 `team-zhu` 这样的本地账号别名。

## Target

target 将某个 profile 映射到文件路径、格式、策略和期望值。plan 从 target 构建，但 target 不会被直接写入。`switch` 会在锁内重建 plan，校验文件 hash，创建备份，然后原子写入目标文件。

## Codex 账号别名

ProfileDeck account id 是保存 Codex `auth.json` payload 的本地别名。Codex `tokens.account_id` 只作为 metadata 保存，因为它不能保证唯一标识每一次本地登录。

## 目标文件

目标文件是外部工具文件，例如：

- `$CODEX_HOME/config.toml`
- `$CODEX_HOME/auth.json`
- 手动配置的 JSON、TOML、env 或 text 文件

ProfileDeck 不会通过 UI 或 CRUD 命令直接写这些文件。写入只发生在 `switch`、`rollback` 或 `recover` 中。
