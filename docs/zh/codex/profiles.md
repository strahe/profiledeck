# Codex Profile

一个 Codex Profile 保存一份登录和一组可复用的 Codex 设置，这组设置称为配置集。Fork 到目标 Profile 时，可以分别选择共享或复制登录与设置。

每个配置集只包含用户级 `config.toml`。会话、日志、Skills、插件缓存、项目 `.codex/config.toml` 和系统策略不在其中。

## 开始前准备

Codex 必须把登录保存在 `auth.json` 中。如果缺少该文件，请在 `$CODEX_HOME/config.toml` 中添加以下设置，然后重新登录：

```toml
cli_auth_credentials_store = "file"
```

请根据要保存的登录方式运行对应的 Codex 命令：

```bash
# ChatGPT
codex login

# OpenAI API Key
printenv OPENAI_API_KEY | codex login --with-api-key

# Codex 访问令牌
printf '%s' "$CODEX_ACCESS_TOKEN" | codex login --with-access-token
```

ProfileDeck 可以保存和切换这些基于文件的登录。仅向 Codex 进程提供 `OPENAI_API_KEY` 或 `CODEX_ACCESS_TOKEN` 不会创建 `auth.json`；需要先运行对应的登录命令，ProfileDeck 才能保存该登录。

ProfileDeck 还需要有效的 `config.toml`。CLI 命令按以下顺序查找 Codex 目录：

1. `--codex-dir`
2. `CODEX_HOME`
3. `~/.codex`

## 在桌面端保存 Profile

1. 选择 **Codex → Profiles**。
2. 选择**保存当前**。
3. 输入创建后不会改变的 Profile ID，以及用于显示的名称。
4. 保存第一个 Profile 时，把当前 Codex 设置保存到默认的 `shared` 配置集。

第一个 Profile 会成为当前 Profile。要保存另一个登录，请运行对应的 Codex 登录命令，返回 ProfileDeck，再保存一个 Profile。如果两个登录应使用相同设置，请复用当前配置集；如果设置需要独立变化，请保存新配置集。

## 使用 CLI 保存 Profile

```bash
profiledeck-cli init
profiledeck-cli codex detect
profiledeck-cli codex profile create work
```

第一个 Profile 会保存当前登录和设置、创建 `shared` 配置集，并成为当前 Profile。后续 Profile 默认复用当前配置集：

```bash
# 请先运行对应的 Codex 登录命令。
profiledeck-cli codex profile create personal
```

需要独立保存当前设置时，运行：

```bash
profiledeck-cli codex profile create client \
  --new-config-set client \
  --config-set-name "Client"
```

## 管理配置集

在桌面端 Codex Profiles 页面打开**配置集**。可以创建、复制、重命名或删除已保存设置。仍有 Profile 使用的配置集不能删除。

对应的 CLI 命令只显示摘要，不会打印完整设置：

```bash
profiledeck-cli codex config-set list
profiledeck-cli codex config-set show shared
profiledeck-cli codex config-set create experimental --name "Experimental"
profiledeck-cli codex config-set copy shared local --name "Local"
profiledeck-cli codex config-set update local --description "Local models"
profiledeck-cli codex config-set delete local --yes
```

为非当前 Profile 选择其他已保存设置：

```bash
profiledeck-cli codex profile set-config work shared
```

## Fork Profile

Fork 会把已保存的 Codex 数据添加到目标 Profile。目标可以是新 Profile，也可以是尚无 Codex 数据的现有 Profile；其中其他 Agent 的数据不会改变。如果目标 Profile 的登录或配置集需要独立变化，请复制对应内容，避免影响来源 Profile。

桌面端会在 Fork 表单中提供共享或复制选项。使用 CLI 时，至少一项必须使用 `copy-new`：

```bash
profiledeck-cli codex profile fork work client-login \
  --credential-binding copy-new \
  --config-binding share-parent

profiledeck-cli codex profile fork work client-config \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client-config
```

## 保存更改并切换

Codex 继续使用普通的 `auth.json` 和 `config.toml` 文件。离开当前 Profile 前，ProfileDeck 会保留当前登录或设置中的有效更改。

如果准备登录其他账号或替换当前文件，并希望先明确保存当前内容，请在桌面端使用**从当前 Codex 更新**，或运行：

```bash
profiledeck-cli codex profile save-current
```

在桌面端选择**使用 Profile**，审核隐藏敏感值的预览，然后确认。使用 CLI 时运行：

```bash
profiledeck-cli switch codex work --dry-run
profiledeck-cli switch codex work --yes
```

`--dry-run` 预览是可选的只读操作。要确保切换内容与之前的预览一致，请传入计划指纹：

```bash
profiledeck-cli switch codex work \
  --plan-fingerprint <fingerprint> \
  --yes
```

当前 `auth.json` 或 `config.toml` 缺失或无效时，预览会提示它不会被保存；确认切换后，ProfileDeck 可以使用所选 Profile 重新创建该文件。当前状态不受支持、无法安全检查或在审核后发生变化时，ProfileDeck 会在写入前停止。请先打开“诊断”或运行 `profiledeck-cli doctor`，再重试。

## 删除 Profile

在桌面端打开 Profile 的操作菜单并选择**删除 Profile**，或运行：

```bash
profiledeck-cli codex profile delete work --yes
```

这会从所有 Agent 中删除完整的全局 Profile，而不只是 Codex 数据。只有该 Profile 使用的已保存登录和配置集也会删除，共享数据会保留。当前 Profile 或存在未完成操作的 Profile 不能删除。删除不会修改 Codex 的 `auth.json`、`config.toml` 或其他工具当前使用的状态。

## 检查限额并保持登录

桌面端可以检查某个已保存 Profile 当前的 ChatGPT Codex 限额。ProfileDeck 会在启动时检查一次当前 Profile；之后需要更新时，请使用**刷新限额**。检查可能会续期受支持的 Codex 登录，并保存刷新后的登录。除非为非当前 Profile 启用了自动间隔，否则不会自动检查它们。

可以在 Profile 详情页或 **Codex → 设置**中，把自动刷新限额设为关闭、5、10、30 或 60 分钟。受支持的 ChatGPT 登录还可以启用**自动续期登录**。两项设置默认关闭，并且只在 ProfileDeck 打开或隐藏到菜单栏时运行。

限额信息只会临时保留，不会保存到磁盘。它不是账单余额，也不会把本地会话关联到某个 Profile 或账号。部分外部登录方式可以显示限额，但无法自动续期。

API Key 和 Codex 访问令牌 Profile 可以保存和切换，但 ProfileDeck 不会检查其 ChatGPT Codex 限额，也不会自动续期这些登录。
