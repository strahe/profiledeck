# 快速开始

保存第一个 Profile 前，先在对应工具中登录。桌面端会自动初始化 ProfileDeck；CLI 用户需要运行一次 `profiledeck-cli init`。

## 在 macOS 上安装桌面端

需要 macOS 14 或更高版本。从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载 Universal DMG 并安装 `ProfileDeck.app`。

如果 macOS 提示应用已损坏或无法验证开发者，请从官方 Releases 页面重新下载，不要绕过安全警告。正式版与 Beta 更新见[更新桌面端](./updates.md)。

## 在 Linux amd64 上安装

Linux 桌面端需要 GTK 4 和 WebKitGTK 6.0。DEB、RPM 同时包含桌面端与 CLI；便携版压缩包只包含桌面端。

### 安装 DEB 或 RPM

从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载对应软件包：

```bash
sudo apt install ./ProfileDeck_<version>_linux_amd64.deb
# 或
sudo dnf install ./ProfileDeck_<version>_linux_amd64.rpm
```

CLI 命令为 `profiledeck-cli`。后续版本仍需用软件包安装；这类构建不会在应用内更新。

### 便携版桌面端

安装运行库，并从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载 Linux tar 压缩包：

```bash
sudo apt install libgtk-4-1 libwebkitgtk-6.0-4
# 或：sudo dnf install gtk4 webkitgtk6.0
```

在当前用户可写的目录中解压并运行：

```bash
mkdir -p "$HOME/.local/opt/profiledeck"
tar -xzf ProfileDeck_<version>_linux_amd64.tar.gz -C "$HOME/.local/opt/profiledeck"
"$HOME/.local/opt/profiledeck/profiledeck"
```

安装目录须保持可写，才能使用[应用内更新](./updates.md)。压缩包不含 CLI。

## 构建并使用 CLI

从源码构建需要 Git、Go 1.27、Make 和 POSIX shell：

```bash
git clone https://github.com/strahe/profiledeck.git
cd profiledeck
make build
export PATH="$PWD/bin:$PATH"
profiledeck-cli init
```

如需使用其他数据位置，在命令前添加 `--config-dir /path/to/config-root`。

## 保存第一个 Profile

不同工具的登录前提如下：

| 工具 | 保存前 | CLI 示例 |
| --- | --- | --- |
| Codex | 准备基于文件的登录和有效的 `config.toml`（[详情](../codex/profiles.md#开始前准备)） | `profiledeck-cli codex profile create work` |
| Claude Code | 运行 `/login`（[详情](../claude-code/profiles.md#开始前准备)） | `profiledeck-cli claude-code profile create personal` |
| Antigravity | 登录 Antigravity（[详情](../antigravity/profiles.md#开始前准备)） | `profiledeck-cli antigravity profile create work` |
| Grok Build | 准备有效的 `auth.json`（[详情](../grok-build/profiles.md#开始前准备)） | `profiledeck-cli grok-build profile create work` |

运行 `profiledeck-cli init` 后，选择其中一条命令。保存另一个账号前，先在对应工具中登录该账号，再创建另一个 Profile。Profile ID 创建后不能修改。

例如，预览并切换 Codex Profile：

```bash
profiledeck-cli switch codex work --dry-run
profiledeck-cli switch codex work --yes
```

`--dry-run` 是可选预览。成功切换不能撤销；影响见[切换说明](../operations/switching.md)。如果切换被阻止或中断，运行 `profiledeck-cli doctor`，并按[恢复说明](../operations/recovery.md)处理。
