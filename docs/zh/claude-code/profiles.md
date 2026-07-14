# Claude Code Profile

一个 Claude Code Profile 保存一份官方订阅登录。ProfileDeck 不会修改 Claude Code 设置、MCP 服务器、插件、API Key、云服务认证或 Claude Desktop。

## 开始前准备

- 桌面端会自动初始化 ProfileDeck；CLI 用户需要先运行一次 `profiledeck init`。
- 保存 Profile 前，先在 Claude Code 中运行 `/login`。
- 使用官方 Pro、Max、Team 或 Enterprise 订阅登录。

ProfileDeck 不会保存 Console/API Key 登录或云服务认证，也不会代替用户完成 Claude Code 登录。

## 在桌面端保存 Profile

1. 选择 **Claude Code → Profiles**。
2. 如果 macOS 要求权限，请选择**授权读取**，允许 ProfileDeck 从 Keychain 读取 Claude Code 登录。
3. 选择**保存当前登录**，然后输入创建后不会改变的 Profile ID 和用于显示的名称。
4. 在 Claude Code 中为另一个账号运行 `/login`，返回 ProfileDeck，再保存一个 Profile。

第一个保存的 Profile 会成为当前 Profile。保存其他 Profile 不会修改 Claude Code 设置。

## 使用 CLI 保存 Profile

登录第一个账号后运行：

```bash
profiledeck claude-code detect
profiledeck claude-code profile create personal --name "Personal"
```

使用 `/login` 登录第二个账号，再单独保存：

```bash
profiledeck claude-code profile create work --name "Work"
profiledeck claude-code profile list
```

`list` 和 `show` 命令只显示登录状态和过期时间，不会打印令牌值。

## 切换账号

在桌面端选择**使用 Profile**，审核登录变化，然后确认。继续前，ProfileDeck 会创建私有备份。

使用 CLI 时运行：

```bash
profiledeck plan claude-code personal
profiledeck switch claude-code personal --yes
```

切换后请新建 Claude Code 会话，并运行 `/status` 确认账号。已经运行的 Claude Code 进程不会改变。

如果 Claude Code 刷新了当前登录，ProfileDeck 会在离开前保存有效更新。即使已保存 Profile 的登录已经过期，仍可以切换到该 Profile，再通过 `/login` 续期。

## 保存刷新后的登录

在桌面端当前 Profile 中使用**保存当前 Claude Code 登录**，或运行：

```bash
profiledeck claude-code profile save-current
```

如果多个 Profile 共用这份登录，ProfileDeck 会显示受影响的 Profile 数量。使用 CLI 时，请先审核该数量，再通过 `--yes` 确认。

## 在 macOS 上允许 Keychain 访问

Claude Code 必须先通过 `/login` 在 Keychain 中创建登录，ProfileDeck 才能保存它。打开 Profiles 页面、运行 `detect` 或打开“诊断”时，ProfileDeck 只会检查登录是否可用。

需要访问权限时，桌面端会显示**授权读取**。随后 macOS 可能要求输入 macOS 登录密码，以允许 ProfileDeck 访问现有的 Claude Code Keychain 条目。这不是在索取 Claude 账号密码。

Keychain 权限按条目分别管理。其他工具无需弹窗，不代表 Claude Code 也不需要授权。

## Linux 和 Windows 登录文件

ProfileDeck 使用 `CLAUDE_CONFIG_DIR` 下的 `.credentials.json`；未设置该变量时，使用 `~/.claude/.credentials.json`。完成首次 Claude Code 设置后，ProfileDeck 会继续使用当时保存的位置。

如果后续 CLI 进程看到不同的 `CLAUDE_CONFIG_DIR`，ProfileDeck 会显示警告，不会静默改用另一个文件。在 Linux 上，ProfileDeck 写入登录文件时会把读取权限限制为当前用户账号。

## Claude Code 使用了错误账号

Claude Code 设置、`apiKeyHelper`、API Key 环境变量和云服务选项可能优先于所选订阅登录。ProfileDeck 会报告自身进程可见的受支持认证覆盖变量名称，但无法检查其他终端或已经运行的 Claude Code 进程。

请新建会话、运行 `/status`，并在所选账号未生效时查阅 [Claude Code 认证文档](https://code.claude.com/docs/en/authentication)。

## 不包含的功能

Claude Code Profile 不包含 Claude Desktop、删除已保存登录、敏感导出/导入、配额检查、用量归属、Console 或 API Key 账号、Claude Code 设置切换或并行账号会话。
