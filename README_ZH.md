<div align="center">

# ProfileDeck

**集中管理和切换 AI Agent Profile**

[![Release](https://img.shields.io/github/v/release/strahe/profiledeck?include_prereleases&label=release)](https://github.com/strahe/profiledeck/releases)
[![License](https://img.shields.io/github/license/strahe/profiledeck)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20amd64-lightgrey)](docs/zh/guide/getting-started.md)

[English](README.md) · [简体中文](README_ZH.md) · [文档](docs/zh/index.md) · [发布](https://github.com/strahe/profiledeck/releases)

</div>

ProfileDeck 将 AI Agent 的登录与配置整理为可复用的 **Profile**，让你在同一处管理 Profile 并查看本地用量。无论使用桌面端还是 CLI，管理的都是同一组本地 Profile。

![ProfileDeck 桌面端 — Codex Profiles](docs/images/desktop-codex-profiles-zh.png)

## 快速开始

| 方式 | 开始使用 |
| --- | --- |
| [**macOS 桌面端**](https://github.com/strahe/profiledeck/releases) | 下载适用于 macOS 14+ 的已签名 Universal 应用 |
| [**Linux 软件包**](docs/zh/guide/getting-started.md#安装-deb-或-rpm) | 在 Linux amd64 上通过 DEB 或 RPM 安装桌面端与 CLI |
| [**Linux 便携版桌面端**](docs/zh/guide/getting-started.md#便携版桌面端) | 在 Linux amd64 用户目录中安装桌面端 |
| [**源码构建 CLI**](docs/zh/guide/getting-started.md#构建并使用-cli) | 构建 `profiledeck`，用于终端工作流与自动化 |

## 核心能力

- **可复用的 Profile** — 按不同工作场景保存 Agent 的登录与配置。
- **切换前确认** — 预览 Profile 将带来的变化，敏感内容保持隐藏。
- **桌面端与 CLI** — 从任一入口管理同一组本地 Profile。
- **本地用量** — 管理 Agent Profile 时，同时查看本地用量、成本估算与使用限额。
- **本地数据** — ProfileDeck 数据保存在本机，并可创建加密应用备份。

## 当前支持的 Agent

目前可使用 Codex、Claude Code 和 Antigravity。详细信息见 [Agent 支持说明](docs/zh/index.md#支持的工具)。

## 文档

- [English manual](docs/index.md)
- [简体中文手册](docs/zh/index.md)
- [CLI 参考](docs/zh/reference/cli.md)
- [数据与安全](docs/zh/reference/data-security.md)

## 许可证

[Apache License 2.0](LICENSE)
