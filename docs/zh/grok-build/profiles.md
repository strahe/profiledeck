# Grok Build Profile

一个 Grok Build Profile 保存一份基于文件的登录和一组可复用的用户设置，这组设置称为配置集。创建其他 Profile 时，可以分别选择共享或复制登录与设置。

ProfileDeck 只管理所选 Grok Home 中的 `auth.json` 和用户级 `config.toml`。会话、日志、插件、项目设置、托管配置及其他 Grok 文件不在其中。

## 开始前准备

先登录 Grok Build，并确认 Grok Home 中存在非空且有效的 `auth.json`。缺少 `config.toml` 不会阻止保存；根据当前文件创建配置集时，ProfileDeck 会将其保存为空设置。

CLI 命令按以下顺序查找 Grok Home：

1. `--grok-home`
2. `GROK_HOME`
3. `~/.grok`

首次成功设置会把 Grok Build Provider 绑定到该绝对路径。之后如果改用其他 Home，ProfileDeck 会拒绝操作，不会静默切换位置。

当 `GROK_AUTH` 或 `GROK_AUTH_PATH` 选择了其他认证来源时，创建 Profile、`save-current` 和切换不可用。执行这些操作前，请取消相关变量并让 Grok Build 使用 `auth.json`。

涉及文件变更时，请先结束活动 Grok 会话，完成后再启动新会话。ProfileDeck 会与 Grok Build 协调 `auth.json` 变更，但不会把进程或锁文件状态当作会话是否运行的依据。

## 在桌面端保存 Profile

1. 选择 **Grok Build → Profiles**。
2. 选择**保存当前**。
3. 输入创建后不会改变的 Profile ID，以及用于显示的名称。
4. 保存第一个 Profile 时使用默认的 `shared` 配置集；只有它尚不存在时，ProfileDeck 才会根据当前设置创建。

第一个 Profile 会成为当前 Profile。要保存另一个登录，请先用 Grok Build 登录该账号，返回 ProfileDeck，再保存一个 Profile。复用当前配置集只会绑定其中已保存的设置，不会读取或覆盖当前 `config.toml`；需要单独保存当前设置时，请新建配置集。

## 使用 CLI 保存 Profile

```bash
profiledeck-cli init
profiledeck-cli grok-build detect
profiledeck-cli grok-build profile create work
```

第一个 Profile 会保存当前登录、使用 `shared` 配置集，并成为当前 Profile。如果 `shared` 尚不存在，ProfileDeck 会根据当前设置创建；预先创建的 `shared` 会保持不变。后续 Profile 默认复用当前 Profile 的已保存配置集，不读取或覆盖当前 `config.toml`：

```bash
grok logout
grok login
profiledeck-cli grok-build profile create personal
```

需要独立保存当前设置时，运行：

```bash
profiledeck-cli grok-build profile create client \
  --new-config-set client \
  --config-set-name "Client"
```

要使用其他 Grok Home，请把全局选项放在命令前：

```bash
profiledeck-cli --grok-home /path/to/grok-home grok-build detect
```

## 管理配置集

在桌面端 Grok Build Profiles 页面打开**配置集**。可以创建、复制、重命名或删除已保存设置。仍有 Profile 使用的配置集不能删除。

对应的 CLI 命令只显示摘要，不会打印 `config.toml`：

```bash
profiledeck-cli grok-build config-set list
profiledeck-cli grok-build config-set show shared
profiledeck-cli grok-build config-set create experimental --name "Experimental"
profiledeck-cli grok-build config-set copy shared local --name "Local"
profiledeck-cli grok-build config-set update local --description "Local settings"
profiledeck-cli grok-build config-set delete local --yes
```

为非当前 Profile 选择其他已保存设置：

```bash
profiledeck-cli grok-build profile set-config work shared
```

## Fork Profile

Fork 会基于已保存数据创建另一个 Profile。如果新 Profile 的登录或配置集需要独立变化，请复制对应内容，避免影响来源 Profile。

桌面端会在 Fork 表单中提供共享或复制选项。使用 CLI 时，至少一项必须使用 `copy-new`：

```bash
profiledeck-cli grok-build profile fork work client-login \
  --credential-binding copy-new \
  --config-binding share-parent

profiledeck-cli grok-build profile fork work client-config \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client-config
```

## 保存更改并切换

Grok Build 继续使用普通的 `auth.json` 和 `config.toml` 文件。离开当前 Profile 前，ProfileDeck 会保留当前登录或设置中的有效更改。请在桌面端使用**从当前 Grok Build 更新**，或运行：

```bash
profiledeck-cli grok-build profile save-current
```

显式保存要求 `auth.json` 非空且有效；认证无效时，不会只保存设置。

在桌面端选择**使用 Profile**，审核操作、目标路径和警告，然后确认。使用 CLI 时运行：

```bash
profiledeck-cli switch grok-build work --dry-run
profiledeck-cli switch grok-build work --yes
```

预览不会包含 `auth.json` 或 `config.toml` 正文。ProfileDeck 会在本地数据中逐字节保存这两个文件，但公开输出只显示操作、目标、路径和警告。

当前工作副本缺失或无效时，ProfileDeck 会警告它不会被保存；确认切换后，仍可恢复所选 Profile 中有效的已保存文件。如果 `config.toml` 包含认证覆盖设置，ProfileDeck 会提示 Grok Build 可能绕过所选的已保存登录，但不会打印该设置，也不会自动修改。

## 删除 Profile

在桌面端打开 Profile 的操作菜单并选择**删除 Profile**，或运行：

```bash
profiledeck-cli grok-build profile delete work --yes
```

这会从所有 Agent 中删除完整的全局 Profile，而不只是 Grok Build 数据。只有该 Profile 使用的已保存登录和配置集也会删除，共享数据会保留。当前 Profile 或存在未完成操作的 Profile 不能删除。删除不会修改 Grok Build 当前使用的文件。

此集成暂不支持 Grok Build 用量、配额、credits 或自定义认证来源。
