# 通用目标文件

通用目标适合需要为尚无专用支持的工具切换本地配置文件的高级用户。Codex 和 Antigravity 用户应使用各自的 Profile 命令；generic target CRUD 不能修改这些托管绑定。

专用流程也负责管理 `codex` 和 `antigravity` Provider 的 adapter 与 metadata。通用 Provider 更新只能更改它们的显示名称或启用状态。

## 创建 provider 和 profile

```bash
profiledeck provider create my-tool --adapter generic --name "My Tool"
profiledeck profile create work --name "Work"
```

## 添加配置文件

```bash
profiledeck profile target add work settings \
  --provider my-tool \
  --path /absolute/path/to/settings.json \
  --format json \
  --strategy json-merge \
  --value-json '{"model":"gpt-5.3-codex"}'
```

目标路径必须是绝对路径。

## 支持的格式和策略

| 策略 | 格式 | `value-json` 结构 |
| --- | --- | --- |
| `replace-file` | `text`, `json`, `toml`, `env` | `{"content":"..."}` |
| `json-merge` | `json` | 合并到目标文件的 JSON object。 |
| `toml-merge` | `toml` | 转换为 TOML 后合并的 JSON object。 |
| `env-merge` | `env` | string value 的 JSON object，会转换为 env assignment。 |

合并策略会在准备预览时读取现有文件。如果文件不是有效的 JSON、TOML 或 env 内容，ProfileDeck 会停止切换并提示你先修复文件。

## 预览和应用

```bash
profiledeck plan my-tool work
profiledeck switch my-tool work --yes
```

预览中的敏感值会被隐藏。出于安全考虑，ProfileDeck 不会修改通过符号链接访问的文件。

## 更新和查看

```bash
profiledeck provider list
profiledeck profile list
profiledeck profile target list work
profiledeck profile target show work my-tool settings
```

这些命令只在 ProfileDeck 中保存文件规则，不会立即修改工具文件。只有运行 `profiledeck switch` 后，文件才会改变。
