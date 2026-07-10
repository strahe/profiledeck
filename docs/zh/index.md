# ProfileDeck

ProfileDeck 用于安全切换本地 AI 编程工具的 Profile。当前实现以 Codex 为主：一个 Profile 组合隐藏登录 credential 与可复用 Config Set，事务流程负责切换工作副本、保留有效本地变化并支持恢复。

## 当前能力

- 从 `$CODEX_HOME/config.toml` 和 `$CODEX_HOME/auth.json` 创建 Codex Profile。
- 在 Profile 之间独立共享 credential 和完整 Config Set。
- 通过带锁、hash guard、备份和原子写入的事务流程切换，并自动捕获有效工作副本变化。
- 导入 Codex session JSONL 用量并显示本地估算成本。
- 查看备份、诊断运行状态、恢复失败切换、回滚已应用切换。
- 管理通用 provider、profile 和 target file，用于高级本地流程。

## 快速开始

```bash
make build

profiledeck init
profiledeck codex detect
profiledeck codex profile create work
profiledeck plan codex work
profiledeck switch codex work --yes
```

Codex profile 切换要求 Codex 使用文件凭据。如果 `$CODEX_HOME/auth.json` 不存在，在 Codex config 中设置 `cli_auth_credentials_store = "file"`，然后重新执行 `codex login`。

## 文档入口

- [快速开始](/zh/guide/getting-started) 说明本地构建、初始化和常用快捷命令。
- [Codex Profile](/zh/codex/profiles) 说明 credential、Config Set 与工作副本语义。
- [切换流程](/zh/operations/switching) 说明 plan、apply、备份和安全检查。
- [数据与安全](/zh/reference/data-security) 说明 secret 存储、备份和脱敏边界。
