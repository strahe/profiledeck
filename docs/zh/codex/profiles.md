# Codex Profile

Codex profile 会恢复日常切换所需的两个用户级文件：

- `$CODEX_HOME/config.toml`
- `$CODEX_HOME/auth.json`

ProfileDeck 不会拆分或移动 `sessions/`、logs、skills 或其他 Codex 状态。这些内容继续在同一个 `CODEX_HOME` 下共享。

内部实现中，profile 保存完整的 `config.toml` 期望内容，并绑定一个隐藏 auth credential。credential 保存最新的 `auth.json` 期望 payload，且可以被多个 profile 共享。Codex `tokens.account_id` 只作为展示 metadata，不作为 ProfileDeck 的 identity 或合并依据。

## 前置条件

Codex 必须使用文件凭据。如果 `$CODEX_HOME/auth.json` 不存在，把下面配置加入 `$CODEX_HOME/config.toml`，然后重新登录：

```toml
cli_auth_credentials_store = "file"
```

再执行：

```bash
codex login
```

## 从当前文件创建 profile

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile create work
```

`create` 要求 `config.toml` 与 `auth.json` 同时存在。它会保存完整的 desired `config.toml` 内容，并为当前 `auth.json` payload 创建一个新的隐藏 credential。

## Fork 或同步 profile

Fork 会复制已有 profile。必须明确选择 fork 是共享源 profile 的 credential 生命周期，还是从复制出的独立 credential 开始：

```bash
profiledeck codex profile fork work client --auth-binding share-parent
profiledeck codex profile fork work client-isolated --auth-binding copy-new
```

Sync 会从当前 Codex 文件更新已有 profile：

```bash
profiledeck codex profile sync work
profiledeck codex profile sync work --auth-update update-shared
profiledeck codex profile sync work --auth-update fork-new
```

当 profile 与其他 profile 共享隐藏 credential 且 `auth.json` 发生变化时，需要明确选择更新共享 credential，或把当前 profile 分叉到新 credential。

## 切换到 profile

```bash
profiledeck plan codex work
profiledeck switch codex work --yes
```

`plan` 只读。`switch` 只通过事务流程写入 `config.toml` 和 `auth.json`。

## 创建另一个登录状态 profile

1. 用另一个 Codex 账号登录，让 `$CODEX_HOME/auth.json` 代表该登录状态。
2. 用不同 profile 创建：

```bash
profiledeck codex profile create personal
```

两个 profile 都存在后即可切换：

```bash
profiledeck switch codex work --yes
profiledeck switch codex personal --yes
```
