# Claude Code 用量与成本

ProfileDeck 从本地可读取的 Claude Code 项目会话和子代理会话统计用量，fork 共享历史只计一次。历史活动无法归属到 Profile、已保存登录或账号。Desktop 运行期间会自动同步，可在 Claude Code 设置中独立调整间隔。

## 同步与查看用量

```bash
profiledeck-cli usage sync claude-code
profiledeck-cli usage sync claude-code --claude-dir /path/to/claude-home
profiledeck-cli usage report --provider claude-code --range 30d
profiledeck-cli usage summary --provider claude-code --json
```

日志目录优先使用 `CLAUDE_CONFIG_DIR`，其次为 `~/.claude`。`--claude-dir` 只改变日志读取位置。同步不读取凭据、不修改源文件；重复同步不会重复累计请求。报告范围可选 `today`、`7d`、`30d`、`all`，日期使用本地时区。

## 理解不完整用量

缺少最终记录时，已验证的输入与缓存 token 仍会计入。总量显示“至少”，缺失输出显示“未知”；后续同步可以补齐请求。恢复历史时产生、且输入、输出和缓存 token 总量已清零的副本会直接跳过，不产生告警。无效候选会跳过；有效最终记录相互矛盾时，该请求不计入 token 与成本总量。报告会显示不完整和冲突的请求数量。数据说明会指出跳过记录的具体原因，例如缓存写入总量与有效期明细之和不一致。跳过记录的数量覆盖全部已扫描日志；请求和费用的数量按选定时间范围统计。

日志被截断或已导入的历史用量被改写时，已接受的报告会保留，同步会显示提示。无法读取、不支持、已删除或未记录的活动无法恢复。统计不包含 Claude Desktop 或云端会话。删除 Claude Code Provider 也会删除其已保存用量；手动同步可重新导入仍存在的日志。

## 理解成本估算

估算依据 [Claude Standard API 价格](https://platform.claude.com/docs/en/about-claude/pricing)，按精确模型和日期匹配。五分钟与一小时缓存写入使用不同费率。缓存时长不明、模型或计价模式不支持时，成本可能部分已知或未知；小计只包含可确定的费用。报告会区分缺少最终输出计数、缺少缓存有效期和缺少缓存费率。输入与缓存的费率已知时，缺少最终输出计数只影响输出费用。

首次获得估算后，补齐缺失用量继续使用当时选定的费率。价格更新不会重新计算已有分类估算。内置价格目录可离线使用，`usage pricing auto off` 可关闭价格目录更新。查看报告不会连接账单服务或上传用量。

这些数字不是账单、订阅费用或账号额度。保存的用量不包含原始请求 ID、提示词、回答、凭据或日志内容，见[本地数据与安全](../reference/data-security.md)。
