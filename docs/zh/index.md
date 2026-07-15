# ProfileDeck

ProfileDeck 把本地 AI 编程工具的登录和设置保存为 Profile。需要使用另一套环境时，可以先审核变更，再确认切换。

## 选择使用方式

| 方式 | 适合场景 | 开始使用 |
| --- | --- | --- |
| macOS 桌面端 | 在一个应用中管理 Profile、用量、更新和恢复操作 | [下载并打开桌面端](./guide/getting-started.md#使用桌面端) |
| CLI | 在终端中使用，或从源码构建后接入自动化流程 | [构建并初始化 CLI](./guide/getting-started.md#构建并使用-cli) |

桌面端 Alpha 要求配备 Apple 芯片的 macOS 14 或更高版本。从源码构建 CLI 需要 Go 1.26 和 Make。

## 支持的工具

| 工具 | ProfileDeck 切换的内容 | 不受影响的内容 |
| --- | --- | --- |
| Codex | 已保存登录和可复用的用户级设置 | 会话、日志、Skills、项目设置和系统策略 |
| Claude Code | 官方订阅登录 | Claude Code 设置、插件、API Key、云服务和 Claude Desktop |
| Antigravity | 个人 OAuth 登录 | 登录流程、设置、配额、Manager 数据以及 SSH 或容器登录文件 |

Codex 用量报告与 Profile 切换相互独立。报告汇总本地会话数据，不会把用量归属到某个 Profile 或账号。

桌面端还可以显示当前 Codex 或 Antigravity Profile 的临时使用限额快照。检查不会改变限额，也不会增加活动归属信息。

## 切换时会发生什么

1. 先审核将要发生的变化。登录内容始终隐藏。
2. 确认切换后，ProfileDeck 会重新检查当前文件或登录。
3. ProfileDeck 会在修改前创建备份。
4. 只有变更成功后，所选 Profile 才会成为当前 Profile。

如果操作没有完成，请先打开“诊断”，或运行 `profiledeck doctor`，再继续切换。

## 继续阅读

- [快速开始](./guide/getting-started.md)
- [了解 Profile、登录与设置](./guide/concepts.md)
- [管理 Codex Profile](./codex/profiles.md)
- [管理 Claude Code Profile](./claude-code/profiles.md)
- [管理 Antigravity Profile](./antigravity/profiles.md)
- [了解数据与安全](./reference/data-security.md)
