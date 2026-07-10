# 核心概念

ProfileDeck 将应用状态和目标工具文件分开管理。

## 应用数据库

`profiledeck.db` 是 ProfileDeck 自有数据的 SQLite source of truth：

- 服务商 (providers)
- 配置集 (profiles)
- 当前激活状态 (active state)
- 配置目标 (profile targets)
- 切换与回滚操作记录 (switch/rollback records)
- Codex 隐藏 auth credential
- Codex Config Set
- 已导入的用量事件 (usage events) 和游标 (cursors)

目标工具文件仍归对应工具所有。ProfileDeck 只会通过 switch 和 rollback 流程写入这些文件。

## Provider

provider 表示一个 AI 工具集成。当前实现的 adapter 是：

- `codex`：用于 Codex profile 切换。
- `generic`：用于手动配置的目标文件。

provider 可以启用或禁用。

## Profile

profile 是一个命名的期望状态。一个 profile 可以包含一个或多个 provider target。Codex Profile 绑定一个隐藏 auth credential 和一个 Config Set；两个资源都可以与其他 Profile 共享。

## Target

target 将某个 profile 映射到文件路径、格式、策略和期望值。plan 从 target 构建，但 target 不会被直接写入。`switch` 会在锁内重建 plan，校验文件 hash，创建备份，然后原子写入目标文件。

## Codex 隐藏 credential

Codex auth credential 是内部生命周期对象，不是用户管理的账号。隐藏 credential 保存最新的 `auth.json` 期望 payload，并可被多个 profile 共享。Codex `tokens.account_id` 只解析为展示 metadata，绝不作为 ProfileDeck identity 或合并依据。

## Codex Config Set

Config Set 保存一份完整的 `$CODEX_HOME/config.toml` 期望 payload。第一个 Codex Profile 会创建 `shared`；该名称可修改，在运行时没有特殊行为。Config Set 属于 ProfileDeck 应用数据，只有未被引用时才能删除。

## Codex 工作副本

Active Profile 的 `auth.json` 与 `config.toml` 是工作副本。切换会先把有效外部变化捕获到 active 绑定，再写入不同的目标绑定；无效或缺失的副本不会被捕获。

## 目标文件

目标文件是外部工具文件，例如：

- `$CODEX_HOME/config.toml`
- `$CODEX_HOME/auth.json`
- 手动配置的 JSON、TOML、env 或 text 文件

ProfileDeck 不会通过 UI 或 CRUD 命令直接写这些文件。写入只发生在 `switch`、`rollback` 或 `recover` 中；create、fork、rebind 和 save-current 只更新 ProfileDeck 应用数据。
