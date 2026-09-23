# CLI 参考

运行 `profiledeck-cli --help` 或 `profiledeck-cli <command> --help` 可查看当前安装版本支持的完整语法。全局选项放在命令前：

- `--config-dir <directory>` 使用 `<directory>/profiledeck` 保存应用数据。
- `--grok-home <directory>` 指定 Grok Build Home，优先于 `GROK_HOME` 和 `~/.grok`。

将尖括号中的值替换为自己的 ID 或路径。

脚本需要结构化结果时，为支持该选项的命令添加 `--json`。

## 初始化与 Profile

```bash
profiledeck-cli init
profiledeck-cli status
profiledeck-cli codex detect
profiledeck-cli codex profile create work
profiledeck-cli codex profile list
profiledeck-cli switch codex work --dry-run
profiledeck-cli switch codex work --yes
```

把 `codex` 换成 `claude-code`、`antigravity` 或 `grok-build`，即可使用对应工具的 Profile 命令。每种工具还提供 `profile show`、`profile save-current` 和 `profile delete`。删除 Profile 会将其从所有工具中移除；见[Profile 与设置](../guide/concepts.md)。切换预览可选。

Codex 和 Grok Build 还提供 `config-set` 管理、`profile set-config` 和 `profile fork`，用于共享或复制登录与设置。实用示例见 [Codex](../codex/profiles.md) 和 [Grok Build](../grok-build/profiles.md)。

## 用量与价格

```bash
profiledeck-cli usage sync codex
profiledeck-cli usage sync grok-build
profiledeck-cli usage summary --provider grok-build
profiledeck-cli usage report --provider grok-build --range 30d
profiledeck-cli usage pricing check
profiledeck-cli usage pricing auto off
```

报告可使用 `--provider codex` 或 `--provider grok-build`，默认是 Codex。范围可选 `today`、`7d`、`30d`、`all`。估算限制见 [Codex](../codex/usage-cost.md) 或 [Grok Build](../grok-build/usage-cost.md)。

## 备份与恢复

```bash
profiledeck-cli doctor
profiledeck-cli backup create
profiledeck-cli backup list
profiledeck-cli backup export <backup-id> --output <私有文件>
profiledeck-cli backup key export --output <私有密钥文件> --yes
profiledeck-cli backup restore <backup-id> --yes
```

备份已加密，但密钥必须单独转移。在目标电脑上运行 `profiledeck-cli backup key import --file <私有密钥文件> --yes`。恢复不会修改工具自己的文件或登录。只有[诊断](../operations/recovery.md)明确建议时，才运行 `recover <operation-id> --yes`、`doctor repair-lock --yes` 或 `doctor retry-cleanup --yes`。

## 其他配置文件

`provider` 和 `profile target` 命令用于其他工具的高级本地文件切换，不能管理 Codex、Claude Code、Antigravity 或 Grok Build 已受管的登录与设置。可运行的示例见[切换其他配置文件](../guide/generic-targets.md)。
