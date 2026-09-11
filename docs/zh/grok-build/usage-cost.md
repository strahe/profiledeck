# Grok Build 用量与成本

ProfileDeck 读取本地 Grok Build 会话记录，展示令牌用量、活动情况和 API 等价成本估算。报告保持离线，也不会把活动归属到某个 Profile、已保存登录或账号。

## 在桌面端同步

桌面端会在启动后同步，并在 ProfileDeck 窗口打开或驻留菜单栏时继续同步。

如需调整间隔，请打开 **Grok Build → 设置 → 用量报告 → 更新频率**，选择 5、15、30 或 60 秒；默认值为 15 秒。Codex 与 Grok Build 分别使用独立的间隔和同步状态。

会话文件没有变化时，后台同步只检查文件信息，不会读取文件内容。文件正常追加后，只读取一小段完整性校验边界和新增部分。

后台同步只使用现有 Grok Build Provider。如果尚未创建，请打开 **Grok Build → Profiles** 创建一个 Profile，或明确运行一次 CLI 同步。

## 使用 CLI 同步

运行：

```bash
profiledeck-cli usage sync grok-build
```

ProfileDeck 按 `--grok-home`、`GROK_HOME`、`~/.grok` 的顺序确定 Grok Home。Provider 已存在时，解析出的位置必须与其中保存的 Grok Home 一致。如需指定位置：

```bash
profiledeck-cli --grok-home /path/to/grok-home usage sync grok-build
```

现有 Provider 绝不会静默改绑到其他 Grok Home。

ProfileDeck 读取以下位置匹配的普通文件：

```text
<grok-home>/sessions/*/*/updates.jsonl
```

它不会跟随符号链接。嵌套的 `subagents` 记录会被排除，因为 Grok Build 已把成功子代理的用量计入完成的父回合。

你可以安全地重复同步，已导入的用量不会再次计数。缺少用量、空用量或用量不完整的记录会被跳过。如果文件在读取期间发生变化，包含过大或格式错误的终态记录，或出现无法识别的终态格式，ProfileDeck 不会提交该文件中的任何新数据。文件发生变化后，后台同步会重新检查；CLI 同步会立即检查。源文件不会被移动或修改。

Fork 复制的历史只计算一次。同一个已完成回合以相同用量出现在多个会话时，ProfileDeck 会将其稳定归入一个派生会话，不保存原始会话标识；如果同一回合的用量冲突，则不会提交受影响的文件，直到文件发生变化或再次运行 CLI 同步。

删除 Grok Build Provider 也会删除已保存的用量报告、导入进度和同步设置。桌面端后台同步不会重新创建已删除的 Provider；再次运行这条 CLI 同步属于明确的用户操作，它可以重新建立 Provider，并重新导入本地会话文件中仍然存在的用量。

## 查看摘要

```bash
profiledeck-cli usage summary --provider grok-build
profiledeck-cli usage summary --provider grok-build --json
```

摘要包含事件数、输入和输出令牌、缓存输入、令牌总量、可用时的成本估算，以及成本未知的事件数。

## 查看报告

```bash
profiledeck-cli usage report --provider grok-build
profiledeck-cli usage report --provider grok-build --range today
profiledeck-cli usage report --provider grok-build --range 30d --json
profiledeck-cli usage report --provider grok-build --range all
```

默认范围是 `7d`。报告使用电脑的本地时区，包含令牌总量、会话数、缓存命中率、已知成本、定价覆盖率、模型明细和同步状态。没有时间戳的记录会计入全量报告的总量和模型明细，其数量会单独显示，但不会出现在时间趋势中。

## 理解成本估算

ProfileDeck 使用当前安装版本内置的 xAI 短上下文标准 API 等价价格：

| 模型 | 输入 | 缓存输入 | 输出 |
| --- | ---: | ---: | ---: |
| `grok-4.5` | $2.00 / 100 万令牌 | $0.30 / 100 万令牌 | $6.00 / 100 万令牌 |
| `grok-4.5-build` | $2.00 / 100 万令牌 | $0.30 / 100 万令牌 | $6.00 / 100 万令牌 |
| `grok-4.5-latest` | $2.00 / 100 万令牌 | $0.30 / 100 万令牌 | $6.00 / 100 万令牌 |
| `grok-build-latest` | $2.00 / 100 万令牌 | $0.30 / 100 万令牌 | $6.00 / 100 万令牌 |

价格来源为 [Grok 4.5](https://docs.x.ai/developers/models/grok-4.5) 和 [xAI 定价](https://docs.x.ai/developers/pricing)。ProfileDeck 将 Grok Build 会话记录中的 `grok-4.5-build` 标识按 Grok 4.5 等价价格计算。聚合后的会话记录无法判断单次请求是否进入长上下文价格阶梯，因此 ProfileDeck 不会应用 2 倍的长上下文价格。

Grok Build 记录的金额不会作为账单数据导入。未识别模型仍会保留令牌总量，但成本未知。后续 ProfileDeck 版本即使调整内置价格，也不会重新计算已有估算；成本未知的事实可在模型被识别后获得估算。

这些估算不是发票、credits、配额或账号余额。同步或生成报告时，ProfileDeck 不会连接 xAI 或计费 API。

## 隐私范围

用量存储不包含原始提示词、代理结果、API 密钥、直接会话标识或完整源文件路径。ProfileDeck 不会上传用量数据，也不会将其用于遥测。存储与备份建议见 [本地数据与安全](../reference/data-security.md)。
