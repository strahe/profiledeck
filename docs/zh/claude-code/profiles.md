# Claude Code Profile

Claude Code Profile 用于保存和切换 Claude Code 官方订阅账号的登录。每个 Profile 绑定一份隐藏登录；Claude Code settings、MCP、插件、API Key、云 Provider 和用量归属不在本功能范围内。

Claude Code 与 Claude Desktop 是两个独立产品。本功能只读取和修改 Claude Code 官方凭据目标，不会探测或修改 Claude Desktop。

## 使用条件

- 先运行 `profiledeck init` 初始化 ProfileDeck。
- 捕获第一个 Profile 前，先在 Claude Code 中通过 `/login` 登录。
- 当前登录必须是包含 `claudeAiOauth` 订阅字段的官方订阅登录。

Console/API Key 登录和云 Provider 认证不会被捕获。ProfileDeck 不负责执行 OAuth 登录。

## 保存两个账号

先在 Claude Code 中登录账号 A，再保存：

```bash
profiledeck claude-code detect
profiledeck claude-code profile create personal --name "个人"
```

在 Claude Code 中通过 `/login` 登录账号 B，再单独保存：

```bash
profiledeck claude-code profile create work --name "工作"
profiledeck claude-code profile list
```

ProfileDeck 为每份登录分配不透明的内部 ID。OAuth 数据中的用户、组织和订阅字段不会用于识别、合并或覆盖已保存登录。

## 切换账号

使用独立的 Provider ID 预览并应用切换：

```bash
profiledeck plan claude-code personal
profiledeck switch claude-code personal --yes
```

切换后请新建 Claude Code 会话，再通过 `/status` 确认账号。ProfileDeck 不会停止或修改已运行的 Claude Code 进程。

如果当前工作登录是 active Profile 已保存登录的新有效版本，切换会先保存该更新，再选择目标 Profile。如果当前工作登录与另一个已知 Profile 匹配，则不会覆盖 active Profile。过期的工作登录不会自动回存，但仍可切换到已保存的过期 Profile，以便在 Claude Code 中通过 `/login` 续期。

需要主动把当前 Claude Code 登录保存到 active Profile 时，运行：

```bash
profiledeck claude-code profile save-current
```

如果隐藏登录被多个 Profile 共用，请先核对受影响 Profile 数量，再通过 `--yes` 确认。

## 凭据位置

ProfileDeck 第一次创建 `claude-code` Provider 时会固定凭据位置：

- macOS：service 为 `Claude Code-credentials`、account 为当前 macOS short username 的唯一 generic-password Keychain 条目；
- Linux 和 Windows：`CLAUDE_CONFIG_DIR` 下的 `.credentials.json`；未设置该变量时使用 `~/.claude/.credentials.json`。

后续命令始终使用已保存的位置。CLI 进程观察到不同的 `CLAUDE_CONFIG_DIR` 时，只会显示非权威警告，不会静默改用另一个目标。

在 macOS 上，必须先由 Claude Code `/login` 创建 Keychain 条目；ProfileDeck 只读取和更新精确定位到的现有条目。Linux 凭据文件固定以 `0600` 写入，并在切换时按需修复该模式。Windows 使用目标目录内的原子替换，不修改目录 DACL。

打开 Claude Code Profiles 页面、运行 `detect` 和运行诊断时，ProfileDeck 只执行不会弹出 macOS 授权窗口的被动 Keychain 检查。如果 macOS 要求授权，Desktop 会显示明确的“授权读取”操作；主动授权、捕获或切换登录时才可能出现系统弹窗。此处应输入 macOS 登录密码，用于允许 ProfileDeck 访问 Keychain，并不是在验证 Claude 账号密码。每个工具的 Keychain 条目有各自独立的访问策略，因此其他集成无需弹窗，并不代表 Claude Code 条目也应当被无条件读取。

## 认证覆盖警告

ProfileDeck 只报告自身进程可见的认证覆盖变量名称，不读取变量值，也无法判断另一个终端或已运行 Claude Code 进程中的环境。

Claude Code settings、`apiKeyHelper`、API Key 变量和云 Provider 开关可能优先于所选订阅登录。如果新会话没有使用预期账号，请查阅 [Claude Code 认证文档](https://code.claude.com/docs/en/team)。

## 当前限制

首版不包含 Claude Desktop、credential 删除、敏感导出/导入、配额、用量归属、Console 或 API Key 账号、Claude Code settings 切换和并行多账号会话。
