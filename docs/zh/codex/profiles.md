# Codex Profile

一个 Codex Profile 保存一份 Codex 登录和一个 Config Set，用于同时切换账号与设置；当前设置仍然可以在 Codex 中正常编辑。

Config Set 只包含用户级 `config.toml`。Sessions、logs、skills、plugin 缓存、项目 `.codex/config.toml` 和系统策略不会被包含。

## 前置条件

Codex 必须使用文件凭据。如果 `$CODEX_HOME/auth.json` 不存在，把下面配置加入 `$CODEX_HOME/config.toml`，然后重新登录：

```toml
cli_auth_credentials_store = "file"
```

```bash
codex login
```

## 创建 Profile

在 Desktop 中选择 **将当前 Codex 配置保存为新 Profile**。Profile ID 用于 CLI 命令和链接，创建后不能修改；名称会显示在 ProfileDeck 各处。

第一个 Profile 会保存当前 Codex 登录和设置，创建名为 `shared` 的 Config Set，并成为当前 Profile：

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile create work
```

后续 Profile 默认复用当前 Config Set。要添加另一个登录而不复制设置，请先用该账号登录 Codex，再创建 Profile：

```bash
codex login
profiledeck codex profile create personal
```

要把当前设置保存为独立 Config Set，请指定新 ID：

```bash
profiledeck codex profile create client \
  --new-config-set client \
  --config-set-name "Client"
```

## 管理 Config Set

Config Set 命令只显示名称和摘要，不会打印完整 Codex 设置：

```bash
profiledeck codex config-set list
profiledeck codex config-set show shared
profiledeck codex config-set create experimental --name "Experimental"
profiledeck codex config-set copy shared local --name "Local"
profiledeck codex config-set update local --description "Local models"
profiledeck codex config-set delete local --yes
```

`create` 会保存当前 `config.toml`。包括 `shared` 在内的 Config Set 都可以重命名；只有未被任何 Profile 使用时才能删除。

为非当前 Profile 选择其他 Config Set：

```bash
profiledeck codex profile set-config work shared
```

## Fork Profile

Fork 会创建新 Profile，并允许共享或复制登录和 Config Set。至少要复制一项，让新 Profile 可以独立修改：

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

切换前，ProfileDeck 会保留当前 Codex 登录和设置中的有效更改。在登录其他账号或替换当前 Codex 文件前，可以使用 `save-current` 显式保存：

```bash
profiledeck codex profile save-current
profiledeck plan codex work
profiledeck switch codex work --yes
```

Desktop 中对应的操作是当前 Profile 详情页里的 **从当前 Codex 更新**。

`plan` 是只读操作，会显示将要改变的文件，并隐藏敏感值。切换前会先创建备份；必需文件缺失、无效、不受支持或在审核后发生变化时，ProfileDeck 会停止切换，不会写入文件。

## 检查使用限额

Desktop 的 Profiles 页面可以检查已保存登录当前的 ChatGPT Codex 限额。ProfileDeck 启动时只检查一次当前 Profile，不会检查其他 Profile，也不会在重新进入页面时重复请求。

之后可以在单个 Profile 行或详情页点击 **刷新限额**。页面不提供“全部刷新”。列表显示各时间段的剩余比例和重置时间；详情页还会显示 Codex 提供的其他限额信息。

登录方式支持续期时，检查限额也可能续期 Codex 登录。部分外部登录方式可以提供限额，但无法自动续期。如果 Codex 自动更新不可用，手动检查限额仍可能以不修改登录的方式工作。

可以在 Profile 详情页或 **Codex 设置** 中，将自动刷新限额设为关闭、5、10、30 或 60 分钟。此设置默认关闭，并会在两个入口之间同步。

托管 ChatGPT 登录还可以启用 **自动续期登录**。自动刷新限额已包含登录续期，因此此选项主要用于关闭限额刷新时。

自动更新只在 ProfileDeck 打开或隐藏到托盘时运行。退出 ProfileDeck 后会停止，也无法在服务撤销登录后继续保持登录。

限额信息只是临时显示，不会写入 `profiledeck.db`，也与本地用量报告相互独立。它不是账单余额，不会把本地 session 归因到某个 Profile 或账号。

## 备份与恢复 Profile

导出前先保存当前更改，并把备份放在准备删除的 ProfileDeck 数据目录之外：

```bash
profiledeck codex profile save-current
profiledeck codex profile export --output ./profiledeck-codex-profiles.json
```

不指定 Profile ID 时，导出包含全部 Codex Profiles 和 Config Sets。指定一个或多个 Profile ID 时，只导出这些 Profiles 及其需要的登录和 Config Sets：

```bash
profiledeck codex profile export work personal \
  --output ./selected-codex-profiles.json
```

JSON 备份包含完整的 Codex 登录数据和设置。ProfileDeck 会在 POSIX 系统上以 `0600` 权限写入文件，命令输出不会打印敏感内容。获得该文件的人可能可以访问你的账号，请妥善保管。

初始化新数据库后，先检查备份，再执行导入：

```bash
profiledeck init
profiledeck codex profile import inspect ./profiledeck-codex-profiles.json
profiledeck codex profile import apply ./profiledeck-codex-profiles.json \
  --plan-fingerprint <reviewed-fingerprint> \
  --yes
```

导入会添加缺失内容、跳过相同内容；已有 ID 的内容不同时，不会写入任何更改。导入不会把 Profile 设为当前、恢复自动更新设置，也不会写入 `auth.json` 或 `config.toml`。准备使用导入的 Profile 时，再审核并应用正常切换。
