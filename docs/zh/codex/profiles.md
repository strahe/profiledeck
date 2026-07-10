# Codex Profile

一个 Codex Profile 由两个可独立共享的资源组成：

- 隐藏 credential，保存期望的 `$CODEX_HOME/auth.json` payload；
- Config Set，保存完整的 `$CODEX_HOME/config.toml` payload。

磁盘文件是 active Profile 的工作副本，长期状态保存在 `profiledeck.db`。切换时，ProfileDeck 会把有效的工作副本变化自动签回 active 绑定，并且只写入绑定发生变化的资源。

Config Set 只覆盖用户级 `config.toml`。Sessions、logs、skills、plugin caches、项目 `.codex/config.toml` 和系统策略不在此模型中。Codex `tokens.account_id` 只用于展示，不参与 identity 或绑定判断。

## 前置条件

Codex 必须使用文件凭据。如果 `$CODEX_HOME/auth.json` 不存在，把下面配置加入 `$CODEX_HOME/config.toml`，然后重新登录：

```toml
cli_auth_credentials_store = "file"
```

```bash
codex login
```

## 创建 Profile

第一个 Profile 会捕获当前文件，创建名为 `shared` 的 Config Set 和一个隐藏 credential，并成为 active Profile：

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile create work
```

后续 Profile 默认复用 active Config Set。先登录另一个 Codex 账号，再创建 Profile，即可捕获独立 credential 而不复制配置：

```bash
codex login
profiledeck codex profile create personal
```

如果要把当前配置保存为独立 Config Set，可在创建 Profile 时指定新 ID：

```bash
profiledeck codex profile create client \
  --new-config-set client \
  --config-set-name "Client"
```

## 管理 Config Set

Config Set 命令只暴露摘要和 metadata，不输出 raw TOML：

```bash
profiledeck codex config-set list
profiledeck codex config-set show shared
profiledeck codex config-set create experimental --name "Experimental"
profiledeck codex config-set copy shared local --name "Local"
profiledeck codex config-set update local --description "Local models"
profiledeck codex config-set delete local --yes
```

`create` 会捕获当前 `config.toml`。包括 `shared` 在内的 Config Set 都可以重命名；只有未被任何 Profile 引用时才能删除。可为 inactive Profile 重新绑定：

```bash
profiledeck codex profile set-config work shared
```

## Fork Profile

Fork 必须显式选择两个资源的共享方式，并且至少复制一项，避免结果只是源 Profile 的别名：

```bash
profiledeck codex profile fork work client-login \
  --credential-binding copy-new \
  --config-binding share-parent

profiledeck codex profile fork work client-config \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client-config
```

## 保存与切换

切换会自动捕获 active credential 和 Config Set 的有效外部变化。`save-current` 是重新登录或替换工作副本前的显式安全操作：

```bash
profiledeck codex profile save-current
profiledeck plan codex work
profiledeck switch codex work --yes
```

`plan` 只读。只有 `switch`、`rollback` 和 `recover` 会写 Codex 目标文件。无效或缺失的工作副本不会被捕获；plan 会给出警告，backup 会保留文件系统现场。
