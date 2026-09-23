# Grok Build 用量与成本

ProfileDeck 从本地 Grok Build 会话汇总令牌用量、API 等价估算和 Grok 上报金额。历史活动无法归属到某个 Profile、已保存登录或账号。桌面端运行时会同步；CLI 可以按需同步。

## 同步用量

```bash
profiledeck-cli usage sync grok-build
profiledeck-cli --grok-home /path/to/grok-home usage sync grok-build
```

所选 Grok Home 必须与此工具已保存的位置一致。重复同步不会重复计数，也不会修改源文件。用量不完整的记录会跳过。如果文件在读取时变化或用量冲突，该文件的新数据不会导入；再次运行 CLI 同步会重新检查。删除 Grok Build Provider 会删除报告和同步设置；明确运行 CLI 同步可重新导入本地仍存在的记录。

## 查看报告

```bash
profiledeck-cli usage summary --provider grok-build
profiledeck-cli usage report --provider grok-build --range 30d
```

报告默认范围为 `7d`，还可选 `today`、`30d` 和 `all`，按电脑本地时区统计。没有时间戳的记录计入全量总数，不进入时间趋势。需要机器可读结果时添加 `--json`。

## 理解成本数值

API 等价估算使用依据 [xAI 标准 API 价格](https://docs.x.ai/developers/pricing)编制的价格表，按模型和记录日期匹配。`grok-4.7-build` 按标准 `grok-4.7` 费率估算。无法核实的模型变体、较早日期或缺少特殊费率详情，可能使估算未知或仅能部分估算。Grok 上报金额单独显示；缺少金额时，小计标为部分上报。两种金额不会相加。会话记录无法判断长上下文价格阶梯，因此估算使用短上下文费率。

价格更新不会重算已有估算；再次同步可补齐此前未知的成本。内置价格表可离线使用。`usage report` 不请求计费服务，也不上传本地用量。

两种金额都不是发票、credits 余额、配额或实际账单费用。[Credits 检查](./profiles.md#查询-credits-额度)覆盖的时间可能与本地会话报告不同。用量存储不包含提示词、代理结果、API Key 或完整源文件路径；详见[数据与安全](../reference/data-security.md)。
