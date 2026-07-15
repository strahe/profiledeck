# 快速开始

使用 macOS 桌面端可以获得可视化操作流程；从源码构建 CLI 后，可以在终端中使用。两种入口共用相同的 Profile、备份和切换规则。

## 开始前准备

- 桌面端 Alpha 要求配备 Apple 芯片的 macOS 14 或更高版本。
- 构建 CLI 需要 Git、Go 1.26、Make 和 POSIX shell。
- 先安装要管理的 AI 编程工具，并在保存第一个 Profile 前完成登录。

## 使用桌面端

1. 从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载最新的 macOS arm64 ZIP。
2. 解压后，把 `ProfileDeck.app` 移到“应用程序”文件夹。
3. 打开 ProfileDeck。应用会自动创建本地数据。如果已有当前 Codex Profile，启动时还会检查其限额；Codex 可能在检查过程中刷新已保存登录。
4. 在侧栏选择 Codex、Claude Code 或 Antigravity，然后打开 **Profiles**。

当前 Alpha 尚未通过 Apple 公证。如果 macOS 阻止首次启动，请打开**系统设置 → 隐私与安全性**，对 ProfileDeck 选择**仍要打开**。只有确认文件来自官方 Releases 页面时才执行此操作。

## 构建并使用 CLI

克隆公开仓库并构建命令：

```bash
git clone https://github.com/strahe/profiledeck.git
cd profiledeck
make build
export PATH="$PWD/bin:$PATH"
profiledeck version
profiledeck init
```

`profiledeck init` 会创建本地数据和备份目录。要使用其他位置，请传入用户配置根目录：

```bash
profiledeck --config-dir /path/to/config-root init
```

ProfileDeck 会在该目录下创建 `profiledeck` 文件夹。

上面的 `export PATH=...` 只会更新当前 shell 的 `PATH`。如需在新终端中继续使用，请把此仓库的 `bin` 目录加入 shell 配置。

## 保存第一个 Profile

先准备要使用的工具：

- **Codex：**确认 `CODEX_HOME` 或 `~/.codex` 中存在 `config.toml` 和 `auth.json`。如果缺少 `auth.json`，请完成 [Codex 前置设置](../codex/profiles.md#开始前准备)。
- **Claude Code：**在 Claude Code 中运行 `/login`，登录官方订阅账号。
- **Antigravity：**登录 Antigravity，并确认可以正常使用。

在桌面端选择工具，然后使用 Profiles 页面中的保存操作。输入创建后不会改变的 Profile ID 和用于显示的名称。要保存另一个账号，请先在对应工具中切换登录，再回到 ProfileDeck 保存另一个 Profile。

也可以使用以下最短 CLI 流程。

### Codex

```bash
profiledeck codex detect
profiledeck codex profile create work
profiledeck plan codex work
profiledeck switch codex work --yes
```

### Claude Code

```bash
profiledeck claude-code detect
profiledeck claude-code profile create personal
profiledeck plan claude-code personal
profiledeck switch claude-code personal --yes
```

切换后请新建 Claude Code 会话，并运行 `/status` 确认账号。

### Antigravity

```bash
profiledeck antigravity detect
profiledeck antigravity profile create work
profiledeck plan antigravity work
profiledeck switch antigravity work --yes
```

条件允许时，请先关闭 Antigravity，切换后再重新启动。

## 确认结果

切换成功后，桌面端会把所选 Profile 标记为**当前**。在 CLI 中，可以查看对应工具的 Profile 列表：

```bash
profiledeck codex profile list
profiledeck claude-code profile list
profiledeck antigravity profile list
```

如果 ProfileDeck 报告未完成的更改，或阻止继续切换，请打开**诊断**，或运行：

```bash
profiledeck doctor
```

只执行诊断功能明确建议的恢复操作。请参阅[恢复操作](../operations/recovery.md)，了解恢复失败操作和撤销成功切换之间的区别。

## 后续步骤

- [Codex Profile](../codex/profiles.md)
- [Claude Code Profile](../claude-code/profiles.md)
- [Antigravity Profile](../antigravity/profiles.md)
- [安全切换](../operations/switching.md)
- [数据与安全](../reference/data-security.md)
