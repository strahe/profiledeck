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

在 Desktop 中，使用 **将当前 Codex 配置保存为新 Profile**。Profile ID 是稳定且不可变的 CLI 与路由键，Name 是面向用户的显示名称。只有当前两个 Codex 文件都有效时，此操作才可用；来源文件缺失或无效时，已保存的 Profiles 仍可查看。

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

Desktop 中对应的操作是活动 Profile 详情页里的 **从当前 Codex 更新**。它会重新校验来源，然后用当前工作副本更新活动隐藏 credential 和 Config Set。

`plan` 只读。只有 `switch`、`rollback` 和 `recover` 会写 Codex 目标文件。无效或缺失的工作副本不会被捕获；plan 会给出警告，backup 会保留文件系统现场。

## 查看使用限额

Desktop 的 Profiles 页面可以读取已保存登录状态当前的 ChatGPT Codex 限额。Desktop 启动并获得 active 状态后，只读取一次 active Profile；不会读取 inactive Profiles，也不会在重新进入页面时重复请求。后续可在单个 Profile 行或详情页点击 **刷新限额**；页面不提供“全部刷新”。

列表显示各限额窗口的剩余百分比和重置时间。详情页还会显示已使用百分比、套餐、限额状态、credits、支出控制、可用重置次数，以及服务返回的其他计量限额。`used_percent` 表示已使用比例，因此剩余比例为 `100 - used_percent`。

手动刷新通常会启动已安装的 `codex app-server`，调用其原生账号限额方法。Codex 可以按自身 token 规则刷新托管 OAuth 登录。Active credential 若产生新的 `auth.json`，ProfileDeck 会按当前绑定的 `credential_id` 签回；inactive credential 会在私有临时 Codex home 中运行，并通过 payload hash compare-and-swap 更新。`tokens.account_id` 仍然只用于展示，不参与归属判断。

如果 app-server 缺失或协议不兼容，手动刷新会回退到固定的只读 ChatGPT Codex 限额端点。回退请求不会刷新或写回 token。Profile 自定义的 model-provider URL 不会收到已保存的 ChatGPT token。

可在 Profile 详情页或 **Codex 设置** 中，把自动限额周期设为关闭、5、10、30 或 60 分钟。两个入口修改同一份持久化设置，并会立即同步。此功能默认关闭。ProfileDeck 同一时间只请求一个 credential，在不同 credentials 之间增加间隔，并按共享 credential 去重；有效周期取所有绑定 Profiles 中最短的已启用周期。首次执行会分散到完整周期内，后续周期会加入时间抖动。

托管 ChatGPT 登录还可以启用 **保持登录可用**。未启用自动限额时，ProfileDeck 会在 access token 临近过期时请求 Codex 刷新；无法读取过期时间时，则按上次刷新后八天调度。外部 `chatgptAuthTokens` 登录可以查询限额，但不支持原生保活。refresh token 已过期、被复用或撤销时，自动任务会暂停到 credential 内容变化；瞬时失败会使用逐级退避。

自动任务只在 ProfileDeck 打开或隐藏到托盘时运行。退出应用后不会继续，也无法在服务端撤销 refresh token 后保持登录。串行原生调用会避免同时批量请求多个 credentials，但不能保证服务端无法关联账号。

限额快照只保存在进程内存中，与 Usage 页面中的离线 session 分析相互独立。它不是账单余额，也不会把本地 session 归因到 Profile 或账号。

## 备份与恢复 Profile

导出前先从当前有效工作副本更新 active Profile，并把 bundle 写到准备删除的 runtime 目录之外：

```bash
profiledeck codex profile save-current
profiledeck codex profile export --output ./profiledeck-codex-profiles.json
```

默认导出包含全部 Codex Profiles、被引用的隐藏 credential，以及包括未绑定配置集在内的全部 Config Sets。传入一个或多个 Profile ID 时，只导出这些 Profiles 及其依赖：

```bash
profiledeck codex profile export work personal \
  --output ./selected-codex-profiles.json
```

JSON bundle 包含 raw `auth.json` 与完整 `config.toml` payload。ProfileDeck 会在 POSIX 系统上以 `0600` 权限写入文件，命令输出不会打印 payload。请把它按敏感文件保管。

初始化新数据库后，先检查导入 plan，再执行应用：

```bash
profiledeck init
profiledeck codex profile import inspect ./profiledeck-codex-profiles.json
profiledeck codex profile import apply ./profiledeck-codex-profiles.json \
  --plan-fingerprint <reviewed-fingerprint> \
  --yes
```

缺失资源会被创建，相同资源会被跳过；任何同 ID 差异都会阻止整次导入。导入会使用当前 `CODEX_HOME`，在一个数据库事务中重建 Profile targets。它不会恢复 active 状态或自动任务设置，也不会写入 `auth.json` 或 `config.toml`；导入后的自动限额和保活均默认关闭。之后仍通过正常的 plan 和 switch 流程激活 Profile。
