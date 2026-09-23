# Codex Profile

Codex Profile 保存一份登录和一组可复用的用户级 `config.toml` 设置（配置集），不包含会话、日志、Skills、插件、项目设置或系统策略。

## 开始前准备

Codex 必须把登录保存在 `auth.json`，并有有效的 `config.toml`。如果缺少 `auth.json`，请在 `$CODEX_HOME/config.toml` 中添加以下设置，然后重新登录：

```toml
cli_auth_credentials_store = "file"
```

按账号类型选择登录命令：

```bash
codex login
printenv OPENAI_API_KEY | codex login --with-api-key
printf '%s' "$CODEX_ACCESS_TOKEN" | codex login --with-access-token
```

仅把 API Key 或访问令牌设为环境变量不会创建 `auth.json`。CLI 按 `--codex-dir`、`CODEX_HOME`、`~/.codex` 的顺序查找 Codex 文件。

## 保存 Profile

```bash
profiledeck-cli codex profile create work
```

第一个 Profile 会保存当前登录，并将设置存入 `shared` 配置集。登录另一个账号后可再创建 Profile；默认复用当前配置集，需要独立设置时指定新配置集：

```bash
profiledeck-cli codex profile create personal
profiledeck-cli codex profile create client --new-config-set client
```

修改共享配置集会影响所有使用它的 Profile。要为非当前 Profile 更换已保存设置，可运行 `profiledeck-cli codex profile set-config <profile-id> <config-set-id>`。共享和删除规则见[Profile 与配置集](../guide/concepts.md)。

## Fork Profile

Fork 可以把 Codex 数据加入新 Profile，或加入尚无 Codex 数据的现有 Profile。登录和设置可分别共享或复制，但至少要复制一项。例如，共享登录、复制设置：

```bash
profiledeck-cli codex profile fork work client \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client
```

## 保存更改并切换

Codex 继续使用普通的 `auth.json` 和 `config.toml`。切离当前 Profile 时，ProfileDeck 会保存有效更改。如果准备登录其他账号或替换这些文件，可以先运行：

```bash
profiledeck-cli codex profile save-current
```

工作文件缺失或无效时，ProfileDeck 会提示不保存该文件；切换仍可用所选 Profile 中有效的文件恢复它。切换命令和恢复行为见[审核并切换](../operations/switching.md)。

## 检查限额并保持登录

桌面端可以检查 ChatGPT Codex 登录和兼容 API Key 服务的限额。启动和切换后会检查当前 Profile。ChatGPT 自动刷新限额和自动续期登录均需主动开启，默认关闭；否则后续检查需手动执行。检查可能续期并保存登录。

对于设置了绝对 HTTP 或 HTTPS 自定义 Base URL 的 API Key Profile，限额检查会把已保存密钥发送到该地址的 `/v1/usage`。API Key 只在启动、切换后或手动操作时检查。HTTP 不会加密传输中的密钥或响应。Codex 访问令牌 Profile 不支持自动刷新限额或登录。

限额快照仅临时保留，与[本地用量报告](./usage-cost.md)分开，也不能判断此前活动属于哪个 Profile。
