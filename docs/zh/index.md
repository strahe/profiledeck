# ProfileDeck

ProfileDeck 把本地 AI 编程工具的登录和设置保存为 Profile。需要使用另一套环境时，可以先审核变更，再确认切换。

## 选择使用方式

| 方式 | 适合场景 | 开始使用 |
| --- | --- | --- |
| macOS 桌面端 | 在一个应用中管理 Profile、更新和恢复 | [在 macOS 上安装桌面端](./guide/getting-started.md#在-macos-上安装桌面端) |
| Linux amd64 软件包 | 同时安装桌面端与 CLI | [安装 DEB 或 RPM](./guide/getting-started.md#安装-deb-或-rpm) |
| Linux 便携版桌面端 | 只要桌面端；用户目录中应用内更新 | [便携版桌面端](./guide/getting-started.md#便携版桌面端) |
| 从源码构建 CLI | 在终端中使用或接入自动化流程 | [构建并使用 CLI](./guide/getting-started.md#构建并使用-cli) |

## 支持的工具

| 工具 | ProfileDeck 切换的内容 | 不受影响的内容 |
| --- | --- | --- |
| Codex | 已保存登录和可复用的用户级设置 | 会话、日志、Skills、项目设置和系统策略 |
| Claude Code | `/login` 账号登录 | Claude Code 设置、插件、API Key、云服务和 Claude Desktop |
| Antigravity | 个人 OAuth 登录 | 登录流程、设置、配额、Manager 数据以及 SSH 或容器登录文件 |
| Grok Build | 基于文件的登录和可复用的用户级设置 | 会话、日志、插件、项目设置、托管配置和配额 |

第一次使用见[快速开始](./guide/getting-started.md)。切换或移动已保存数据前，请阅读[切换说明](./operations/switching.md)和[数据与安全](./reference/data-security.md)。
