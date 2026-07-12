# ProfileDeck

ProfileDeck 用于安全切换本地 AI 编程工具的 Profile。当前实现以 Codex 为主：每个 Profile 保存一份 Codex 登录和一个可复用的 Config Set，用于同时切换账号与设置。

## 当前能力

- 从 `$CODEX_HOME/config.toml` 和 `$CODEX_HOME/auth.json` 创建 Codex Profile。
- 在 Profile 之间独立共享已保存登录和 Config Set。
- 切换前审核全部变更、保留 Codex 中的有效修改并创建备份；审核后文件发生变化时会停止切换。
- 导入当前和归档 Codex session JSONL，并分析本地时间趋势、模型、会话、缓存用量和 API 等价估算成本。
- 查看备份、找出阻止切换的问题、恢复失败切换，并撤销已完成的切换。
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

- [快速开始](/zh/guide/getting-started) 说明本地构建、首次设置和常用命令。
- [Codex Profile](/zh/codex/profiles) 说明已保存登录、Config Set、切换、限额和备份。
- [Codex 用量与成本](/zh/codex/usage-cost) 说明离线导入、分析报告和估算限制。
- [切换流程](/zh/operations/switching) 说明 plan、apply、备份和安全检查。
- [数据与安全](/zh/reference/data-security) 说明 secret 存储、备份和脱敏边界。
