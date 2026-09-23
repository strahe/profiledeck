# 切换其他配置文件

通用目标是高级 CLI 功能，用于切换用户指定的本地配置文件。它不能管理 Codex、Claude Code、Antigravity 或 Grok Build 的登录与设置；这些工具应使用各自的 Profile 命令。

请使用普通文件的绝对路径；不支持符号链接。先决定替换整个文件还是合并部分值；可能包含敏感内容时，请仔细审核预览。

## 保存文件目标

```bash
profiledeck-cli init
profiledeck-cli provider create my-tool --adapter generic --name "My Tool"
profiledeck-cli profile create work --name "Work"
profiledeck-cli profile target add work settings \
  --provider my-tool \
  --path /absolute/path/to/settings.json \
  --format json \
  --strategy json-merge \
  --value-json '{"model":"example-model"}'
```

| 策略 | 格式 | `--value-json` |
| --- | --- | --- |
| `replace-file` | `text`、`json`、`toml`、`env` | `{"content":"..."}`，替换整个文件。 |
| `json-merge` | `json` | 合并到文件中的 JSON 对象。 |
| `toml-merge` | `toml` | 转为 TOML 后合并的 JSON 对象。 |
| `env-merge` | `env` | 转为变量赋值的字符串 JSON 对象。 |

合并前，现有文件内容必须有效。添加或编辑目标只更改 ProfileDeck 保存的规则；切换成功后才会修改外部文件。

## 切换文件

```bash
profiledeck-cli switch my-tool work --dry-run
profiledeck-cli switch my-tool work --yes
```

预览可选，会隐藏疑似敏感值。切换失败时，运行 `profiledeck-cli doctor` 并按[恢复说明](../operations/recovery.md)处理。成功切换不能撤销。
