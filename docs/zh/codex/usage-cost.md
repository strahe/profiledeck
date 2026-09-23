# Codex 用量与成本

ProfileDeck 从本地 Codex 会话汇总令牌用量，并估算标准 API 等价成本。历史活动无法归属到某个 Profile、已保存登录或 ChatGPT 账号。桌面端运行时会同步；CLI 可以按需同步。

## 同步用量

```bash
profiledeck-cli usage sync codex
profiledeck-cli usage sync codex --codex-dir /path/to/codex-home
```

默认读取 `CODEX_HOME` 或 `~/.codex` 下的会话。重复同步不会重复计数。无效或不支持的记录会跳过并报告，不会修改源文件。删除 Codex Provider 会删除已保存的用量报告；再次明确运行 CLI 同步，可重新导入本地仍存在的记录。

## 查看报告

```bash
profiledeck-cli usage summary
profiledeck-cli usage report --range 30d
```

报告默认范围为 `7d`，还可选 `today`、`30d` 和 `all`，按电脑本地时区统计。没有时间戳的记录计入全量总数，不进入时间趋势。需要机器可读结果时添加 `--json`。

## 理解成本估算

ProfileDeck 使用依据 [OpenAI 标准 API 价格](https://developers.openai.com/api/docs/pricing)编制的价格表，按准确模型名称和记录日期匹配。无法确认的模型别名、没有已核实价格的日期，以及缺少特殊费率所需详情的记录，成本可能未知或仅能部分估算。令牌总量和已知成本小计仍会显示。价格更新不会重算已有估算；再次同步可补齐此前未知的成本。

内置价格表可离线使用。`usage report` 不请求计费服务，也不上传本地用量。检查或关闭价格更新：

```bash
profiledeck-cli usage pricing check
profiledeck-cli usage pricing auto off
```

估算不是发票、订阅费用、账号限额或 ChatGPT 余额。[限额检查](./profiles.md#检查限额并保持登录)与用量报告分开。用量存储不包含提示词、回复、API Key 或完整源文件路径；详见[数据与安全](../reference/data-security.md)。
