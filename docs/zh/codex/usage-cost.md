# Codex 用量与成本

ProfileDeck 读取本地 Codex 会话数据，展示令牌用量、活动情况和 API 等价成本估算。报告保持离线，也不会把会话归属到某个 Profile 或 ChatGPT 账号。

## 在桌面端同步

桌面端会在启动后同步，并在 ProfileDeck 窗口打开或驻留菜单栏时继续同步。

如需调整间隔，请打开 **Codex → 设置 → 用量报告 → 更新频率**，选择 15 秒、30 秒、1 分钟、2 分钟或 5 分钟；默认值为 1 分钟。用量页面会显示最近一次同步结果，并报告无法读取的文件。

会话文件没有变化时，后台同步只检查文件信息，不会读取文件内容。文件正常追加后，只读取一小段完整性校验边界和新增部分。如果现有文件被截短，或 ProfileDeck 检测到之前的用量记录发生变化，它会保留已导入的历史并跳过该文件版本；文件再次变化或手动运行 CLI 同步后会重新检查。

## 使用 CLI 同步

运行：

```bash
profiledeck-cli usage sync codex
```

ProfileDeck 默认读取：

```text
$CODEX_HOME/sessions/**/*.jsonl
$CODEX_HOME/archived_sessions/*.jsonl
```

如果没有设置 `CODEX_HOME`，则使用 `~/.codex`。如需读取其他 Codex 主目录：

```bash
profiledeck-cli usage sync codex --codex-dir /path/to/codex-home
```

你可以安全地重复同步，已导入的用量不会再次计数。无效、过大或不支持的记录会被跳过并报告，但其内容不会被保存。

删除 Codex Provider 也会删除其已保存用量报告。桌面端后台同步不会重新创建已删除的 Provider；再次运行这条 CLI 同步属于明确的用户操作，它可以重新建立 Codex Provider，并重新导入本地 Codex 日志中仍然存在的用量。

## 查看摘要

```bash
profiledeck-cli usage summary
profiledeck-cli usage summary --json
```

摘要包含事件数、输入和输出令牌、缓存输入、令牌总量、可用时的成本估算，以及成本未知的事件数。

## 查看报告

```bash
profiledeck-cli usage report
profiledeck-cli usage report --range today
profiledeck-cli usage report --range 30d --json
profiledeck-cli usage report --range all
```

默认范围是 `7d`。可用范围如下：

- `today`：当前本地自然日，按小时分组；
- `7d`：今天和之前六个本地自然日；
- `30d`：今天和之前 29 个本地自然日；
- `all`：跨度不超过 36 个月时按月分组，超过后按年分组。

报告使用电脑的本地时区，包含令牌总量、会话数、缓存命中率、已知成本、定价覆盖率、模型明细和同步状态。没有时间戳的记录会计入全量报告的总量和模型明细，其数量会单独显示，但不会出现在时间趋势中。

## 理解成本估算

ProfileDeck 根据 [OpenAI 标准 API 价格](https://developers.openai.com/api/docs/pricing)估算成本，按每条记录的准确模型名称和发生日期选择价格。早于已核实价格的记录，或模型名称为 `chat-latest` 等可变别名的记录，成本保持未知。价格更新后，已有估算不会改变；再次同步可补齐此前未知的成本。因此，一份报告可能包含价格更新前后的估算。

- `estimated`：所选用量都有可用价格；
- `partial`：只能估算所选用量中的一部分；
- `unknown`：至少一条所选记录没有可用价格。

报告始终保留令牌总量，并显示已知成本小计。定价覆盖率表示所选令牌用量中可估价的比例。

如果本地记录缺少应用缓存写入或长上下文费率所需的详情，ProfileDeck 会显示部分估算。

应用启动时会检查价格更新，运行期间最多每 24 小时检查一次。CLI 在价格表需要更新时，会先检查再执行 `usage sync`；检查失败不会中断同步。你可以在 **设置 → 用量价格** 中单独管理自动检查，也可以使用：

```bash
profiledeck-cli usage pricing status
profiledeck-cli usage pricing check
profiledeck-cli usage pricing auto off
profiledeck-cli usage pricing auto on
```

离线时可使用内置价格表。`usage report` 不会联网。ProfileDeck 只下载价格表，不会上传本地用量。

这些数值不是订阅账单、账号限额、发票或 ChatGPT 余额。生成用量报告时，ProfileDeck 不会请求计费 API。[Codex 限额查询](./profiles.md#检查限额并保持登录)是独立功能，不会改变用量报告，也不会为报告归属账号。

## 隐私范围

用量存储不包含原始提示词、原始回复、API 密钥或完整源文件路径。ProfileDeck 不会上传用量数据，也不会将其用于遥测。存储与备份建议见[本地数据与安全](../reference/data-security.md)。
