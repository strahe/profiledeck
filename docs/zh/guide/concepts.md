# 核心概念

ProfileDeck 保存需要一起切换的 AI 编程工具登录与设置。

## Provider

Provider 表示一种 AI 工具集成。ProfileDeck 当前支持：

- `codex`：引导式 Codex 工作流；
- `antigravity`：Antigravity agy v2 登录 Profile；
- `generic`：管理用户明确选择的本地文件，适合高级工作流。

Desktop 侧栏把 Codex 和 Antigravity 称为 Agent。Provider 是底层数据与切换命名空间；Agent 是 UI 中明确支持的工具工作区。

## Profile

Profile 是全局的命名组合对象，可以参与多个 Provider 工作流。一个 Codex Profile 包含：

- 一份已保存的 Codex 登录；
- 一个 Config Set，保存与该登录一起使用的 Codex 设置。

登录和 Config Set 可以分别共享。例如，两个 Profile 可以使用相同设置和不同登录，也可以使用相同登录和不同设置。

一个 Antigravity Profile 包含一份已保存的 agy v2 登录。Antigravity 工作区只显示具有该托管绑定的 Profile。

## 当前 Profile

当前 Profile 按 Provider 分别记录。Codex working copy 仍是普通 `auth.json` 和 `config.toml` 文件；Antigravity working login 位于系统凭据存储中。

切换前，ProfileDeck 会保留当前 Codex 文件中的有效更改。必需文件缺失或无效时，ProfileDeck 会显示警告，不会静默保存这些内容。

## 已保存登录

已保存登录包含一个或多个 Profile 使用的工具登录数据。它是隐藏的生命周期资源，不是 ProfileDeck 中单独管理的账号。

ProfileDeck 可能显示 Codex Account ID 的末尾字符，帮助区分不同登录。这个值仅用于展示，不会决定更新或共享哪一份登录。

## Config Set

Config Set 包含一份完整的用户级 Codex 配置。第一个 Profile 会创建名为 `shared` 的 Config Set；可以重命名、复制，或为需要不同设置的 Profile 新建独立 Config Set。

只有未被任何 Profile 使用的 Config Set 才能删除。

## Codex 文件

ProfileDeck 使用：

- `$CODEX_HOME/config.toml`：当前 Codex 设置；
- `$CODEX_HOME/auth.json`：当前 Codex 登录。

Skills、plugin 缓存、项目 `.codex/config.toml`、sessions、logs 和系统策略不属于 Config Set。

只有在用户审核并应用切换、回滚或恢复操作后，ProfileDeck 才会修改这些文件。创建、编辑、Fork 或导入 Profile 只会改变 ProfileDeck 中已保存的数据。

## ProfileDeck 本地数据

Profiles、Config Sets、已保存登录、设置、用量报告和操作历史都保存在本地 `profiledeck.db` 中。目标工具仍然拥有自己的文件和系统凭据存储条目。
