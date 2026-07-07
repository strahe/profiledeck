# Codex 账号切换

完整 Codex 账号切换会捕获并恢复两个用户级文件：

- `$CODEX_HOME/config.toml`
- `$CODEX_HOME/auth.json`

ProfileDeck 不会拆分或移动 `sessions/`、logs、skills 或其他 Codex 状态。这些内容继续在同一个 `CODEX_HOME` 下共享。

## 前置条件

Codex 必须使用文件凭据。如果 `$CODEX_HOME/auth.json` 不存在，把下面配置加入 `$CODEX_HOME/config.toml`，然后重新登录：

```toml
cli_auth_credentials_store = "file"
```

再执行：

```bash
codex login
```

## 捕获当前账号

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile capture work
```

默认情况下，本地 ProfileDeck account id 等于 profile id。只有需要不同本地别名时才使用 `--account`：

```bash
profiledeck codex profile capture work --account work-login
```

本地 account id 不等于 Codex `tokens.account_id`。ProfileDeck 只把 `tokens.account_id` 保存为 metadata。

## 切换到已捕获 profile

```bash
profiledeck plan codex work
profiledeck switch codex work --yes
```

`plan` 只读。`switch` 只通过事务流程写入 `config.toml` 和 `auth.json`。

## 捕获另一个账号

1. 用另一个 Codex 账号登录，让 `$CODEX_HOME/auth.json` 代表该账号。
2. 用不同 profile 捕获：

```bash
profiledeck codex profile capture personal
```

两个 profile 都捕获后即可切换：

```bash
profiledeck switch codex work --yes
profiledeck switch codex personal --yes
```

## 管理已保存账号

列出和查看账号 metadata：

```bash
profiledeck codex account list
profiledeck codex account show work
```

这些命令不会打印 raw token。

export/import 是显式敏感操作：

```bash
profiledeck codex account export work --output ./auth.json
profiledeck codex account import work-edited --auth-file ./auth.json
```

export 会在平台支持时把 raw Codex auth JSON 写到 `0600` 文件。导出的文件应按 secret 处理。
