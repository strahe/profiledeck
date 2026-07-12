# Codex 用量与成本

ProfileDeck 导入本地 Codex 活动，并按时间、模型和 session 数提供离线分析。它不会查询账号限额，也不会猜测某个 session 来自哪个 Profile 或 ChatGPT 账号。Desktop Profile 页面的限额检查是独立功能，不会改变用量报告。

## Desktop 自动同步

Desktop 启动后会立即同步一次，并在 ProfileDeck 保持运行或隐藏到托盘时继续后台同步。可以在 **Codex > 设置 > 本地用量同步间隔** 中选择 5、15、30 或 60 秒，默认值为 15 秒。

同步不会重叠。如果上一次扫描尚未结束，当前周期会被跳过。失败后会在下一个周期重试。Usage 页面只显示最新状态，不会反复弹出通知。

## CLI 手动同步

```bash
profiledeck usage sync codex
```

默认扫描：

```text
$CODEX_HOME/sessions/**/*.jsonl
$CODEX_HOME/archived_sessions/*.jsonl
```

如果没有设置 `CODEX_HOME`，则回退到 `~/.codex`。使用 `--codex-dir` 可以指定 Codex home：

```bash
profiledeck usage sync codex --codex-dir /path/to/codex-home
```

重复同步不会重复计算相同用量，包括移动或复制到 `archived_sessions` 的 session。Fork session 包含来源 session 的早期用量时，ProfileDeck 只计算一次早期用量，并单独保留 Fork 后的新用量。无效、过大或不支持的记录会被跳过并计数，不会保存其内容。

## 汇总

```bash
profiledeck usage summary
profiledeck usage summary --json
```

summary 包含：

- event count
- input tokens
- cached input tokens
- output tokens
- total tokens
- 所有事件都能完整估价时的 USD 估算成本
- unknown cost event count

如果任一记录的成本未知或只有部分估算，JSON 输出中的 `estimated_cost_usd` 为 null，cost status 为 `unknown`。部分成本详情和已知小计请使用 report。

## 分析报告

```bash
profiledeck usage report
profiledeck usage report --range today
profiledeck usage report --range 30d --json
profiledeck usage report --range all
```

默认范围是 `7d`，可选范围如下：

- `today`：本地当前自然日，按小时分桶；
- `7d`：包含今天在内的七个本地自然日；
- `30d`：包含今天在内的三十个本地自然日；
- `all`：跨度不超过 36 个月时按月，否则按年。

自然日边界使用机器本地时区，并正确处理夏令时。趋势会补齐空桶。没有时间戳的记录会进入全量汇总和模型统计，单独报告数量，但不进入趋势。

报告包含记录数、唯一 session 数、新输入与缓存输入、输出、总 Tokens、缓存命中率、已知成本小计、按 Token 加权的定价覆盖率、模型统计和导入状态。Desktop Usage 页面默认显示已知 API 等价成本趋势，也可以切换到 Token 趋势。鼠标悬停或键盘聚焦某个时间段时，会显示准确数值。

## 估算限制

成本按精确模型名称，以及导入或回填事件时应用版本内置的 [OpenAI Standard API 价格](https://developers.openai.com/api/docs/pricing)静态表进行本地估算。Provider 前缀、日期版本和其他未明确列出的别名不会映射到已定价模型。如果模型或价格未知，ProfileDeck 会保留 token 数并把成本标记为 `unknown`。

GPT-5.6 Sol、Terra 和 Luna 具有独立的 [cache-write 价格](https://developers.openai.com/api/docs/guides/prompt-caching#requirements)，但 Codex session 日志没有提供 cache-write token 数量。因此，ProfileDeck 只计算其 Standard input、cached-input 和 output 基础成本，无法估价可能存在的 cache-write 特有部分，并将受影响事件标记为 `partial`。

报告始终展示已知成本小计。只要选中范围内存在未知价格记录，整体状态就是 `unknown`；否则，只要有成本部分缺失，状态就是 `partial`。定价覆盖率表示具有已知价格的 Tokens 占比。

这些数值只是 API 等价估算，不是订阅账单、账号限额或真实 ChatGPT 余额。用量报告不会查询 Provider 账单 API、余额或 relay 服务。Profile 限额检查相互独立，不会用于账单或 session 归因。
