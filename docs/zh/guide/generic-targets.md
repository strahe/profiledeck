# 切换其他配置文件

通用目标是高级 CLI 功能，用于切换用户明确选择的本地配置文件。Codex、Claude Code 和 Antigravity 必须使用各自的 Profile 命令；通用目标命令不能修改这些工具管理的登录或设置。

## 开始前准备

- 运行 `profiledeck init` 初始化 ProfileDeck。
- 使用普通本地文件的绝对路径。
- 确定要替换整个文件，还是只合并指定值。

ProfileDeck 不会修改通过符号链接访问的文件。如果目标文件包含敏感值，请仔细审核预览。

## 创建工具和 Profile

```bash
profiledeck provider create my-tool --adapter generic --name "My Tool"
profiledeck profile create work --name "Work"
```

Provider ID 用于在后续命令中标识工具；Profile ID 用于标识要切换到的已保存设置。

## 添加配置文件

```bash
profiledeck profile target add work settings \
  --provider my-tool \
  --path /absolute/path/to/settings.json \
  --format json \
  --strategy json-merge \
  --value-json '{"model":"example-model"}'
```

## 选择文件修改方式

| 策略 | 格式 | `--value-json` 提供的内容 |
| --- | --- | --- |
| `replace-file` | `text`、`json`、`toml`、`env` | `{"content":"..."}`，替换整个文件。 |
| `json-merge` | `json` | 合并到当前 JSON 文件的 JSON 对象。 |
| `toml-merge` | `toml` | 转换为 TOML 后合并的 JSON 对象。 |
| `env-merge` | `env` | 转换为环境变量赋值的字符串 JSON 对象。 |

使用合并策略时，当前文件必须是有效的 JSON、TOML 或 env 内容。内容无效时，请先修复文件，再执行切换。

## 审核并切换

```bash
profiledeck plan my-tool work
profiledeck switch my-tool work --yes
```

预览会显示所选文件，并隐藏疑似敏感值。应用前，ProfileDeck 会再次检查文件并创建操作恢复点。

## 查看或恢复

```bash
profiledeck provider list
profiledeck profile list
profiledeck profile target list work
profiledeck profile target show work my-tool settings
```

添加或编辑目标时，只会修改已保存规则。只有 `profiledeck switch` 成功后，外部文件才会改变。

如果切换没有完成，请先运行 `profiledeck doctor`。恢复切换前状态的方法见[诊断与恢复](../operations/recovery.md)。成功切换不能撤销；如需更换配置，请切换到目标 Profile。
