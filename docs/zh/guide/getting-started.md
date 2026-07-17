# 快速开始

使用 macOS 桌面端可以获得可视化操作流程；从源码构建 CLI 后，可以在终端中使用。两种入口共用相同的 Profile、应用备份、操作恢复和切换规则。

## 开始前准备

- Universal 桌面端要求 macOS 14 或更高版本，支持 Apple 芯片和 Intel Mac。
- 构建 CLI 需要 Git、Go 1.26、Make 和 POSIX shell。
- 先安装要管理的 AI 编程工具，并在保存第一个 Profile 前完成登录。

## 使用桌面端

1. 从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载最新的 macOS Universal DMG。正式版使用 `X.Y.Z`，Beta 版使用 `X.Y.Z-beta.N`。
2. 打开 DMG，把 `ProfileDeck.app` 拖到“应用程序”文件夹。
3. 打开 ProfileDeck。应用会自动创建本地数据。如果已有当前 Codex 或 Antigravity Profile，启动时还会检查其限额。Codex 可能在检查过程中刷新已保存登录；Antigravity 检查只读取数据。
4. 在侧栏选择 Codex、Claude Code 或 Antigravity，然后打开 **Profiles**。

发布的 DMG 已使用 Developer ID 签名并通过 Apple 公证。如果 macOS 提示应用已损坏或无法验证开发者，请删除该副本，并从官方 Releases 页面重新下载，不要绕过安全警告。

已签名的桌面端可以在**设置 → 应用更新**中选择接收正式版或 Beta 更新。本地开发构建不会检查更新。

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

`profiledeck init` 会创建本地数据库、加密应用备份目录和操作恢复目录。要使用其他位置，请传入用户配置根目录：

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

只执行诊断功能明确建议的恢复操作。未完成切换恢复和应用备份恢复见[诊断与恢复](../operations/recovery.md)。成功切换不能撤销。

## 后续步骤

- [Codex Profile](../codex/profiles.md)
- [Claude Code Profile](../claude-code/profiles.md)
- [Antigravity Profile](../antigravity/profiles.md)
- [安全切换](../operations/switching.md)
- [数据与安全](../reference/data-security.md)
