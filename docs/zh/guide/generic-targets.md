# 通用目标文件

generic targets 是高级本地流程的底层构建块。大多数 Codex 用户应该优先使用 `profiledeck codex profile capture` 或 `profiledeck codex profile set`。

## 创建 provider 和 profile

```bash
profiledeck provider create my-tool --adapter generic --name "My Tool"
profiledeck profile create work --name "Work"
```

## 添加 target

```bash
profiledeck profile target add work settings \
  --provider my-tool \
  --path /absolute/path/to/settings.json \
  --format json \
  --strategy json-merge \
  --value-json '{"model":"gpt-5.3-codex"}'
```

目标路径 (target path) 必须是绝对路径。

## 支持的格式和策略

| 策略 | 格式 | `value-json` 结构 |
| --- | --- | --- |
| `replace-file` | `text`, `json`, `toml`, `env` | `{"content":"..."}` |
| `json-merge` | `json` | 合并到目标文件的 JSON object。 |
| `toml-merge` | `toml` | 转换为 TOML 后合并的 JSON object。 |
| `env-merge` | `env` | string value 的 JSON object，会转换为 env assignment。 |

merge 策略在 plan 阶段需要读取现有文件内容。现有 JSON、TOML 或 env 内容无效时，plan 会失败。

## 预览和应用

```bash
profiledeck plan my-tool work
profiledeck switch my-tool work --yes
```

plan 会显示脱敏 preview 和 SHA-256 hash。符号链接 target 不会被跟随，会被报告为 unsupported。

## 更新和查看

```bash
profiledeck provider list
profiledeck profile list
profiledeck profile target list work
profiledeck profile target show work my-tool settings
```

CRUD 命令只更新 ProfileDeck 状态，不写目标文件。
