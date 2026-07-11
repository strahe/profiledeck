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

`profiledeck.db` 保存 ProfileDeck 应用数据。对 Codex 来说，它会在隐藏 credential 记录中保存 raw `auth.json` payload，在 Config Set 中保存完整 `config.toml` payload，并保存每个 Profile 的本机自动任务设置。Credential 与 Config Set 是资源的长期状态；`$CODEX_HOME` 中的文件是 active Profile 的工作副本。账号限额快照不会写入数据库。

每个 Config Set 限制为 16 MiB，并校验 SHA-256 hash。即使数据库列名不表示 secret，payload 仍可能包含 token 或其他敏感配置。

数据库不会做静态加密。在 POSIX 系统上，ProfileDeck 会尽力收紧文件权限，但本地文件系统访问权限仍然是安全边界。

## 备份

switch 和 rollback 备份可能包含之前的目标文件内容。对 Codex 来说，这可能包括 raw `auth.json` 和 `config.toml`。

backup 命令展示 path、action、hash 和 mode 等 metadata，不会在常规输出中打印 raw auth content。

## 敏感 Profile 导出

`profiledeck codex profile export` 是用于本地备份和数据库重建的显式敏感导出模式。它的 JSON bundle 包含 raw Codex `auth.json` 与完整 Config Set payload。ProfileDeck 要求显式指定输出路径，采用原子写入，拒绝 symlink 目标，并在 POSIX 系统上把文件权限设置为 `0600`。它不会创建或修改用户选择的父目录。

导入只接受私有的普通文件。写入前会校验格式、版本、hash、auth JSON、TOML、引用和冲突，然后在一个事务中应用全部数据库变更。它不会使用 `tokens.account_id` 合并 credential，不会包含本机自动任务设置，也不会设置 active 状态或写 Codex 工作文件。导入后的 Profile 默认关闭自动限额刷新和登录保活。

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

## Codex 限额与登录保活

手动刷新和显式启用的自动任务通常使用已安装的 `codex app-server`。ProfileDeck 会初始化短生命周期的 stdio session，并调用 Codex 原生账号方法。该进程会关闭 remote plugins、apps、analytics、memories 和 app instructions，避免启动无关网络功能。Profile 自定义的 model-provider URL 不会收到隐藏 ChatGPT token。

Active credential 会在 ProfileDeck 持有共享 switch lock 时使用真实 `CODEX_HOME`。如果 Codex 轮换托管 OAuth token，ProfileDeck 会读取更新后的 `auth.json`，并按当前 `credential_id` 签回绑定的 credential。Inactive credential 使用临时 `CODEX_HOME`，目录权限为 `0700`，`auth.json` 权限为 `0600`；只有原 credential hash 未变化时才会更新数据库，并发产生的新 credential 内容优先。`tokens.account_id` 不参与 identity、归属、去重或 token 更新判断。

如果 app-server 不可用或协议不兼容，手动刷新单个 Profile 可以回退到固定的只读 ChatGPT Codex 限额端点。该回退不会刷新或写回 OAuth token。自动限额和登录保活不会使用此回退。

自动网络任务默认关闭，并且只在 Desktop/托盘进程运行时生效。全局串行 worker 同一时间只处理一个 credential，在不同 credentials 之间增加间隔，并对共享 credential 去重。托管 token 保活使用 Codex 原生刷新路径。外部 `chatgptAuthTokens` credential 可以查询限额，但不能原生保活；API key 等其他登录方式不支持。

限额快照只保存在进程内存。运行时事件与 Desktop DTO 只包含 Profile ID、时间、下次执行时间、结果状态和映射后的限额快照，不包含 token、临时路径、credential payload hash 或 raw app-server error。

## ProfileDeck 不保存什么

ProfileDeck usage import 保存派生的 token 与 cost 记录、校验后的模型标签、用于唯一计数的安全或派生 session 标识、稳定的 event identity hash，以及基于路径 hash 的 cursor key。稳定 event identity 先按 provider 和 source 隔离，再由安全 session 标识、session 内用量顺序、模型和 token 数派生；它不包含路径、时间戳、prompt 或 completion。ProfileDeck 不会把 raw Codex JSONL events、prompts、completions、完整来源路径或 API keys 作为 usage metadata 或报告输出持久化。导入错误最多暴露 basename、hash 后的 source key 和已清理的文件系统错误。

用量报告只暴露聚合结果。本地日志无法可靠识别实际服务请求的 Profile、隐藏 credential 或 ChatGPT account，因此 ProfileDeck 不推断或发布账号级用量。

Desktop 自动同步状态只包含配置间隔、时间戳、结果状态、聚合错误数量，以及按需提供的脱敏错误 code/message；不会向 UI 事件流发送 session 路径、cursor key 或 raw importer error。

Config Set v1 不捕获 skills、plugin 安装缓存、项目 `.codex/config.toml` 或系统策略。
