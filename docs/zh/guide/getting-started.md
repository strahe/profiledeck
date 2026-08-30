# 快速开始

使用桌面端可以获得可视化操作流程；CLI 适合终端操作。两种入口共用相同的 Profile、应用备份、操作恢复和切换规则。

## 开始前准备

- Universal 桌面端要求 macOS 14 或更高版本，支持 Apple 芯片和 Intel Mac。
- Linux 发布支持 amd64；桌面端需要 GTK 4 和 WebKitGTK 6.0。
- 构建 CLI 需要 Git、Go 1.27、Make 和 POSIX shell。
- 先安装要管理的 AI Agent，并在保存第一个 Profile 前完成登录。

## 在 macOS 上安装桌面端

1. 从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载最新的 macOS Universal DMG。正式版使用 `X.Y.Z`，Beta 版使用 `X.Y.Z-beta.N`。
2. 打开 DMG，把 `ProfileDeck.app` 拖到“应用程序”文件夹。
3. 打开 ProfileDeck。应用会自动创建本地数据。如果已有当前 Codex 或 Antigravity Profile，启动时还会检查其限额。Codex 可能在检查过程中刷新已保存登录；Antigravity 检查只读取数据。
4. 在侧栏选择 Codex、Claude Code、Antigravity 或 Grok Build，然后打开 **Profiles**。

发布的 DMG 已使用 Developer ID 签名并通过 Apple 公证。如果 macOS 提示应用已损坏或无法验证开发者，请删除该副本，并从官方 Releases 页面重新下载，不要绕过安全警告。

macOS 桌面端可在**设置 → 常规 → 应用更新**中选择正式版或 Beta 更新。本地开发构建不会检查更新。详见[更新桌面端](./updates.md)。

## 在 Linux amd64 上安装

请选择一种安装方式：

- **DEB 或 RPM 软件包：**同时安装桌面端与 CLI；新版软件包从 GitHub Releases 手动安装。
- **便携版桌面端：**仅桌面端；请放在当前用户可写的目录，以便应用内更新。详见[更新桌面端](./updates.md)。

### 安装 DEB 或 RPM

正式版和 Beta 版 DEB、RPM 都会安装 `profiledeck` 桌面端与 `profiledeck-cli` 命令，添加桌面启动项，并声明 GTK 4 与 WebKitGTK 6.0 依赖。DEB 会在 Ubuntu 24.04 上做安装冒烟测试，RPM 会在 Fedora 44 上测试。

在 Ubuntu 24.04 上，从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载 `ProfileDeck_<version>_linux_amd64.deb`，然后安装：

```bash
sudo apt install ./ProfileDeck_<version>_linux_amd64.deb
```

在 Fedora 44 上，下载 `ProfileDeck_<version>_linux_amd64.rpm`，然后安装：

```bash
sudo dnf install ./ProfileDeck_<version>_linux_amd64.rpm
```

从应用菜单打开 ProfileDeck，或运行 `profiledeck`。CLI 位于 `/usr/bin/profiledeck-cli`，已在 `PATH` 中。

这类安装不会在应用内检查或下载更新。请从 GitHub Releases 下载新版对应软件包，再用相同命令安装。详见[更新 Linux 软件包](./updates.md#更新-linux-软件包)。

接下来请[保存第一个 Profile](#保存第一个-profile)。

### 便携版桌面端

适合只要桌面端，并由应用内更新的场景。

1. 先安装运行库：

```bash
sudo apt install libgtk-4-1 libwebkitgtk-6.0-4
# 或：sudo dnf install gtk4 webkitgtk6.0
```

2. 从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载 `ProfileDeck_<version>_linux_amd64.tar.gz`。

3. 解压并链接到当前用户可写的目录：

```bash
mkdir -p "$HOME/.local/opt/profiledeck" "$HOME/.local/bin"
tar -xzf ProfileDeck_<version>_linux_amd64.tar.gz -C "$HOME/.local/opt/profiledeck"
ln -sf "$HOME/.local/opt/profiledeck/profiledeck" "$HOME/.local/bin/profiledeck"
```

4. 运行 `profiledeck`（请确保 `$HOME/.local/bin` 在 `PATH` 中）。

压缩包只包含 `profiledeck`，不含 CLI。需要 `profiledeck-cli` 命令时请使用 DEB 或 RPM。请把安装目录保持为可写，以便 ProfileDeck 在验证更新后替换程序。详见[更新桌面端](./updates.md)。

接下来请[保存第一个 Profile](#保存第一个-profile)。

## 构建并使用 CLI

克隆公开仓库并构建命令：

```bash
git clone https://github.com/strahe/profiledeck.git
cd profiledeck
make build
export PATH="$PWD/bin:$PATH"
profiledeck-cli version
profiledeck-cli init
```

`profiledeck-cli init` 会创建本地数据库、加密应用备份目录和操作恢复目录。要使用其他位置，请传入用户配置根目录：

```bash
profiledeck-cli --config-dir /path/to/config-root init
```

ProfileDeck 会在该目录下创建 `profiledeck` 文件夹。

上面的 `export PATH=...` 只会更新当前 shell 的 `PATH`。如需在新终端中继续使用，请把此仓库的 `bin` 目录加入 shell 配置。

## 保存第一个 Profile

先准备要使用的工具：

- **Codex：**确认 `CODEX_HOME` 或 `~/.codex` 中存在 `config.toml` 和 `auth.json`。如果缺少 `auth.json`，请完成 [Codex 前置设置](../codex/profiles.md#开始前准备)。
- **Claude Code：**在 Claude Code 中运行 `/login`。
- **Antigravity：**登录 Antigravity，并确认可以正常使用。
- **Grok Build：**登录后确认 `GROK_HOME` 或 `~/.grok` 中存在非空且有效的 `auth.json`。详见 [Grok Build 前置设置](../grok-build/profiles.md#开始前准备)。

在桌面端选择工具，然后使用 Profiles 页面中的保存操作。输入创建后不会改变的 Profile ID 和用于显示的名称。要保存另一个账号，请先在对应工具中切换登录，再回到 ProfileDeck 保存另一个 Profile。

也可以使用以下 CLI 流程。

每组流程中的 `--dry-run` 命令都是可选预览；使用 `--yes` 应用切换。

### Codex

```bash
profiledeck-cli codex detect
profiledeck-cli codex profile create work
profiledeck-cli switch codex work --dry-run
profiledeck-cli switch codex work --yes
```

### Claude Code

```bash
profiledeck-cli claude-code detect
profiledeck-cli claude-code profile create personal
profiledeck-cli switch claude-code personal --dry-run
profiledeck-cli switch claude-code personal --yes
```

切换后请新建 Claude Code 会话，并运行 `/status` 确认账号。

### Antigravity

```bash
profiledeck-cli antigravity detect
profiledeck-cli antigravity profile create work
profiledeck-cli switch antigravity work --dry-run
profiledeck-cli switch antigravity work --yes
```

条件允许时，请先关闭 Antigravity，切换后再重新启动。

### Grok Build

```bash
profiledeck-cli grok-build detect
profiledeck-cli grok-build profile create work
profiledeck-cli switch grok-build work --dry-run
profiledeck-cli switch grok-build work --yes
```

保存或切换文件前，请先结束活动 Grok 会话，完成后再启动新会话。

## 确认结果

切换成功后，桌面端会把所选 Profile 标记为**当前**。在 CLI 中，可以查看对应工具的 Profile 列表：

```bash
profiledeck-cli codex profile list
profiledeck-cli claude-code profile list
profiledeck-cli antigravity profile list
profiledeck-cli grok-build profile list
```

如果 ProfileDeck 报告未完成的更改，或阻止继续切换，请打开**诊断**，或运行：

```bash
profiledeck-cli doctor
```

只执行诊断功能明确建议的恢复操作。未完成切换恢复和应用备份恢复见[诊断与恢复](../operations/recovery.md)。成功切换不能撤销。

## 后续步骤

- [Codex Profile](../codex/profiles.md)
- [Claude Code Profile](../claude-code/profiles.md)
- [Antigravity Profile](../antigravity/profiles.md)
- [Grok Build Profile](../grok-build/profiles.md)
- [更新桌面端](./updates.md)
- [安全切换](../operations/switching.md)
- [数据与安全](../reference/data-security.md)
