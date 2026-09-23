# Grok Build Profile

Grok Build Profile 保存 `auth.json` 中基于文件的登录和用户级 `config.toml` 设置，不包含会话、日志、插件、项目设置或托管配置。

## 开始前准备

先用 Grok Build 登录，并确认 `auth.json` 存在、非空且有效。如果创建新配置集时缺少 `config.toml`，其中的设置为空。修改文件前请结束活动 Grok 会话，完成后再新建会话。

Grok Home 按 `--grok-home`、`GROK_HOME`、`~/.grok` 的顺序选择，首次设置后位置固定。如果 `GROK_AUTH` 或 `GROK_AUTH_PATH` 选择了其他认证来源，则不能创建 Profile、保存或切换。

## 保存 Profile

```bash
profiledeck-cli grok-build profile create work
```

第一个 Profile 使用 `shared` 配置集；需要时根据当前设置创建。后续 Profile 复用当前 Profile 已保存的配置集，不读取正在使用的 `config.toml`。要单独保存当前设置：

```bash
profiledeck-cli grok-build profile create client --new-config-set client
```

使用其他 Grok Home 时，将全局选项放在命令前：`profiledeck-cli --grok-home /path/to/grok-home grok-build profile create work`。

Fork 时可分别共享或复制登录与配置集，但至少复制其中一项。例如，共享登录、复制设置：

```bash
profiledeck-cli grok-build profile fork work client \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client
```

共享和删除的影响见[Profile 与设置](../guide/concepts.md)。

## 保存更改并切换

切离当前 Profile 时，ProfileDeck 会保存有效的登录和设置更改。替换正在使用的文件前，可以先运行：

```bash
profiledeck-cli grok-build profile save-current
```

显式保存要求 `auth.json` 有效且非空，并且 `config.toml` 存在且有效；空的 `config.toml` 仍有效。任一检查失败时，两项已保存内容都不会改变。切换仍可恢复所选 Profile 中有效的文件。预览会隐藏两个文件的正文。`config.toml` 中的认证覆盖设置可能绕过所选登录；ProfileDeck 会警告，但不会修改该设置。详见[审核并切换](../operations/switching.md)。

## 查询 credits 额度

桌面端在启动和切换后检查当前 Profile，之后需要手动检查；非当前 Profile 不能刷新。检查使用已安装的 Grok Build，可能续期当前登录。结果仅临时保留，与[本地用量报告](./usage-cost.md)分开，也不是账号账单。

检查需要受支持的已保存登录和可用的 Grok Build 安装；不支持 `GROK_AUTH`、`GROK_AUTH_PATH`。如果安装时使用了 `GROK_BIN_DIR`，请让 ProfileDeck 也能访问同一个绝对目录。
