# ProfileDeck

ProfileDeck 用于安全切换本地 AI 编程工具的 profile。当前实现以 Codex 为主：可以捕获 Codex 用户配置和文件形式的授权信息，在已保存的 profile 之间切换，导入本地 token 用量，并从中断的切换操作中恢复。

## 当前能力

- 从 `$CODEX_HOME/config.toml` 和 `$CODEX_HOME/auth.json` 捕获 Codex profile。
- 通过带锁、hash guard、备份和原子写入的事务流程切换 Codex `config.toml` 与 `auth.json`。
- 在不捕获 auth 的情况下管理轻量级 Codex model/base URL profile。
- 导入 Codex session JSONL 用量并显示本地估算成本。
- 查看备份、诊断运行状态、恢复失败切换、回滚已应用切换。
- 管理通用 provider、profile 和 target file，用于高级本地流程。

## 快速开始

```bash
make build

profiledeck init
profiledeck codex detect
profiledeck codex profile capture work
profiledeck plan codex work
profiledeck switch codex work --yes
```

完整账号切换要求 Codex 使用文件凭据。如果 `$CODEX_HOME/auth.json` 不存在，在 Codex config 中设置 `cli_auth_credentials_store = "file"`，然后重新执行 `codex login`。

## 文档入口

- [快速开始](/zh/guide/getting-started) 说明本地构建、初始化和常用快捷命令。
- [账号切换](/zh/codex/account-switching) 说明 Codex 完整 profile 工作流。
- [切换流程](/zh/operations/switching) 说明 plan、apply、备份和安全检查。
- [数据与安全](/zh/reference/data-security) 说明 secret 存储、备份和脱敏边界。
