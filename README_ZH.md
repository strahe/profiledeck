<div align="center">

# ProfileDeck

**保存并切换 AI Agent 的 Profile**

[![Release](https://img.shields.io/github/v/release/strahe/profiledeck?include_prereleases&label=release)](https://github.com/strahe/profiledeck/releases)
[![License](https://img.shields.io/github/license/strahe/profiledeck)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](docs/zh/guide/getting-started.md)

[English](README.md) · [简体中文](README_ZH.md) | [文档](docs/zh/index.md) · [参与贡献](CONTRIBUTING.md) · [安全政策](SECURITY.md) · [发行版](https://github.com/strahe/profiledeck/releases)

</div>

ProfileDeck 将 AI Agent 的登录信息和设置保存为可复用的 **Profile**。你可以通过桌面端或 CLI 在不同 Profile 之间切换，并提前查看变更内容。

## 支持的 Agent

ProfileDeck 支持 Codex、Claude Code、Antigravity 和 Grok Build。各工具可切换的内容见[支持的工具](docs/zh/index.md#支持的工具)。

## 安装

| 安装方式 | 包含内容 | 详情 |
| --- | --- | --- |
| **macOS 应用** | 桌面端 | [前往 Releases 下载](https://github.com/strahe/profiledeck/releases) |
| **Linux DEB 或 RPM** | 桌面端与 CLI | [安装 Linux 软件包](docs/zh/guide/getting-started.md#安装-deb-或-rpm) |
| **Linux 便携版** | 桌面端 | [安装便携版桌面端](docs/zh/guide/getting-started.md#便携版桌面端) |
| **源码构建** | CLI | [构建并使用 CLI](docs/zh/guide/getting-started.md#构建并使用-cli) |

## CLI 示例

以名为 `work` 的 Codex Profile 为例：

```bash
profiledeck-cli codex profile list
profiledeck-cli switch codex work --yes
profiledeck-cli usage summary --provider codex
```

脚本需要机器可读结果时，可在支持的命令中添加 `--json`。常用命令见 [CLI 参考](docs/zh/reference/cli.md)；完整语法运行 `profiledeck-cli --help` 查看。

## 许可证

[Apache License 2.0](LICENSE)
