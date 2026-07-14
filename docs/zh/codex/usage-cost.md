# Codex 用量与成本

ProfileDeck 读取本地 Codex 会话数据，展示令牌用量、活动情况和 API 等价成本估算。报告保持离线，也不会把会话归属到某个 Profile 或 ChatGPT 账号。

## 在桌面端同步

桌面端会在启动后同步，并在 ProfileDeck 窗口打开或驻留菜单栏时继续同步。

如需调整间隔，请打开 **Codex → 设置 → 用量报告 → 更新频率**，选择 5、15、30 或 60 秒；默认值为 15 秒。用量页面会显示最近一次同步结果，并报告无法读取的文件。

## 使用 CLI 同步

运行：

```bash
profiledeck usage sync codex
```

ProfileDeck 默认读取：

```text
$CODEX_HOME/sessions/**/*.jsonl
$CODEX_HOME/archived_sessions/*.jsonl
```

如果没有设置 `CODEX_HOME`，则使用 `~/.codex`。如需读取其他 Codex 主目录：

```bash
profiledeck usage sync codex --codex-dir /path/to/codex-home
```

你可以安全地重复同步，已导入的用量不会再次计数。无效、过大或不支持的记录会被跳过并报告，但其内容不会被保存。

## 查看摘要

```bash
profiledeck usage summary
profiledeck usage summary --json
```

摘要包含事件数、输入和输出令牌、缓存输入、令牌总量、可用时的成本估算，以及成本未知的事件数。

## 查看报告

```bash
profiledeck usage report
profiledeck usage report --range today
profiledeck usage report --range 30d --json
profiledeck usage report --range all
```

默认范围是 `7d`。可用范围如下：

- `today`：当前本地自然日，按小时分组；
- `7d`：今天和之前六个本地自然日；
- `30d`：今天和之前 29 个本地自然日；
- `all`：跨度不超过 36 个月时按月分组，超过后按年分组。

报告使用电脑的本地时区，包含令牌总量、会话数、缓存命中率、已知成本、定价覆盖率、模型明细和同步状态。没有时间戳的记录会计入全量报告的总量和模型明细，其数量会单独显示，但不会出现在时间趋势中。

## 理解成本估算

ProfileDeck 根据准确的模型名称，以及每条记录首次获得成本估算时所安装版本包含的 [OpenAI 标准 API 价格](https://developers.openai.com/api/docs/pricing)估算成本。安装新版本不会重新计算已有估算。

- `estimated`：所选用量都有可用价格；
- `partial`：只能估算所选用量中的一部分；
- `unknown`：至少一条所选记录没有可用价格。

报告始终保留令牌总量，并显示已知成本小计。定价覆盖率表示所选令牌用量中可估价的比例。

这些数值不是订阅账单、账号限额、发票或 ChatGPT 余额。生成用量报告时，ProfileDeck 不会请求计费 API。[Codex 限额查询](./profiles.md#检查限额并保持登录)是独立功能，不会改变用量报告，也不会为报告归属账号。

## 隐私范围

用量存储不包含原始提示词、原始回复、API 密钥或完整源文件路径。ProfileDeck 不会上传用量数据，也不会将其用于遥测。存储与备份建议见[本地数据与安全](../reference/data-security.md)。
