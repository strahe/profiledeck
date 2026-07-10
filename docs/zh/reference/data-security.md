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

## 敏感 Profile 导出

`profiledeck codex profile export` 是用于本地备份和数据库重建的显式敏感导出模式。它的 JSON bundle 包含 raw Codex `auth.json` 与完整 Config Set payload。ProfileDeck 要求显式指定输出路径，采用原子写入，拒绝 symlink 目标，并在 POSIX 系统上把文件权限设置为 `0600`。它不会创建或修改用户选择的父目录。

导入只接受私有的普通文件。写入前会校验格式、版本、hash、auth JSON、TOML、引用和冲突，然后在一个事务中应用全部数据库变更。它不会使用 `tokens.account_id` 合并 credential，也不会设置 active 状态或写 Codex 工作文件。

删除开发数据库前，请把敏感 bundle 放到 ProfileDeck runtime 之外。不要提交或分享该文件。

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

导出和导入命令的输出仍然只有 metadata；只有用户显式选择的 bundle 文件包含 raw payload。

## ProfileDeck 不保存什么

ProfileDeck usage import 保存派生的 token 与 cost 记录、校验后的模型标签、用于唯一计数的安全或派生 session 标识、稳定的 event identity hash，以及基于路径 hash 的 cursor key。稳定 event identity 先按 provider 和 source 隔离，再由安全 session 标识、session 内用量顺序、模型和 token 数派生；它不包含路径、时间戳、prompt 或 completion。ProfileDeck 不会把 raw Codex JSONL events、prompts、completions、完整来源路径或 API keys 作为 usage metadata 或报告输出持久化。导入错误最多暴露 basename、hash 后的 source key 和已清理的文件系统错误。

用量报告只暴露聚合结果。本地日志无法可靠识别实际服务请求的 Profile、隐藏 credential 或 ChatGPT account，因此 ProfileDeck 不推断或发布账号级用量。

Desktop 自动同步状态只包含配置间隔、时间戳、结果状态、聚合错误数量，以及按需提供的脱敏错误 code/message；不会向 UI 事件流发送 session 路径、cursor key 或 raw importer error。

Config Set v1 不捕获 skills、plugin 安装缓存、项目 `.codex/config.toml` 或系统策略。
