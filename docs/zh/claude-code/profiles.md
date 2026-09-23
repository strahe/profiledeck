# Claude Code Profile

ProfileDeck 只保存 Claude Code `/login` 创建的账号登录，不代替你登录，也不管理 API Key、Console 或云服务认证、Claude Code 设置及 Claude Desktop。

## 开始前准备

先在 Claude Code 中运行 `/login`；需要使用 CLI 时，再运行一次 `profiledeck-cli init`。在 macOS 上，ProfileDeck 可能需要读取 Claude Code Keychain 条目的权限。macOS 要求输入的是电脑登录密码，不是 Claude 账号密码。

## 保存和切换账号

```bash
profiledeck-cli claude-code profile create personal
```

要保存另一个账号，先在 Claude Code 中为该账号运行 `/login`，再创建另一个 Profile。第一个已保存 Profile 会成为当前 Profile。切换使用[共通的切换命令](../operations/switching.md)。切换后请新建 Claude Code 会话，并运行 `/status` 确认账号；已运行的进程不会改变。

切离当前 Profile 时，ProfileDeck 会保存有效的刷新登录。若要在再次运行 `/login` 前保存，可运行：

```bash
profiledeck-cli claude-code profile save-current
```

如果多个 Profile 共用这份登录，请先核对受影响数量，再使用 `--yes` 确认。共享和删除的影响见[Profile 与设置](../guide/concepts.md)。

## 登录位置和认证覆盖

在 Linux 和 Windows 上，ProfileDeck 使用 `CLAUDE_CONFIG_DIR/.credentials.json`；未设置该变量时使用 `~/.claude/.credentials.json`。首次设置后会固定该位置，后续 CLI 进程指向其他位置时会警告。

Claude Code 设置、`apiKeyHelper`、API Key 环境变量或云服务选项可能优先于所选账号。若账号不符，请新建会话、运行 `/status`，并查阅 [Claude Code 认证文档](https://code.claude.com/docs/en/authentication)。ProfileDeck 无法检查其他终端的环境或已经运行的 Claude Code 进程。
