# Grok Build 用量与成本

ProfileDeck 读取本地 Grok Build 会话记录，展示令牌用量、活动情况、API 等价成本估算和 Grok 上报成本。报告保持离线，也不会把活动归属到某个 Profile、已保存登录或账号。

## 在桌面端同步

桌面端会在启动后同步，并在 ProfileDeck 窗口打开或驻留菜单栏时继续同步。

如需调整间隔，请打开 **Grok Build → 设置 → 用量报告 → 更新频率**，选择 15 秒、30 秒、1 分钟、2 分钟或 5 分钟；默认值为 1 分钟。Codex 与 Grok Build 分别使用独立的间隔和同步状态。

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

Grok Build 会话记录新增的字段会被安全忽略；已知字段仍必须使用预期类型，令牌总量也必须保持一致。

Fork 复制的历史只计算一次。同一个已完成回合以相同用量出现在多个会话时，ProfileDeck 会将其稳定归入一个派生会话，不保存原始会话标识；如果同一回合的用量冲突，则不会提交受影响的文件，直到文件发生变化或再次运行 CLI 同步。

删除 Grok Build Provider 也会删除已保存的用量报告、导入进度和同步设置。桌面端后台同步不会重新创建已删除的 Provider；再次运行这条 CLI 同步属于明确的用户操作，它可以重新建立 Provider，并重新导入本地会话文件中仍然存在的用量。

## 查看摘要

```bash
profiledeck-cli usage summary --provider grok-build
profiledeck-cli usage summary --provider grok-build --json
```

摘要包含事件数、输入和输出令牌、缓存输入、令牌总量、可用时的 API 等价估算和 Grok 上报成本，以及两种金额各自成本未知的事件数。

## 查看报告

```bash
profiledeck-cli usage report --provider grok-build
profiledeck-cli usage report --provider grok-build --range today
profiledeck-cli usage report --provider grok-build --range 30d --json
profiledeck-cli usage report --provider grok-build --range all
```

默认范围是 `7d`。报告使用电脑的本地时区，包含令牌总量、会话数、缓存命中率、API 等价估算与 Grok 上报成本的小计及各自覆盖率、模型明细和同步状态。没有时间戳的记录会计入全量报告的总量和模型明细，其数量会单独显示，但不会出现在时间趋势中。

## 理解成本估算

ProfileDeck 使用参考 [xAI 标准 API 价格](https://docs.x.ai/developers/pricing)的价格表，按模型和记录发生日期选择费率。Grok Build 的 `grok-4.7-build` 记录会按标准版 `grok-4.7` 费率计算 API 等价估算。Fast 变体、其他内部 `*-build` 标识和可变的 `*-latest` 别名，在无法核实其准确模型映射时仍保持未知。会话记录无法判断单次请求是否进入长上下文价格阶梯，因此估算使用短上下文费率。

如果会话包含缓存创建令牌，且该模型有已核实的短上下文费率，ProfileDeck 会显示部分估算。

ProfileDeck 还会显示 Grok 在已完成会话记录中上报的金额。如果部分调用没有上报金额，则显示已知金额的小计，并标记为部分上报。只有金额完整的记录才计入上报成本覆盖率；如果所选记录均无上报金额，则上报成本不可用。

API 等价估算与 Grok 上报金额分开显示，不会相加。模型无法识别或记录早于已核实价格时，令牌用量和 Grok 上报金额仍会保留，API 等价成本则为未知。价格更新后，已有估算不会改变；再次同步可补齐此前未知的成本。因此，一份报告可能包含价格更新前后的估算。

应用启动时会检查价格更新，运行期间最多每 24 小时检查一次。CLI 在价格表需要更新时，会先检查再执行 `usage sync`；检查失败不会中断同步。你可以在 **设置 → 用量价格** 中单独管理自动检查，也可以使用 `profiledeck-cli usage pricing status`、`check` 和 `auto on|off`。离线时可使用内置价格表，`usage report` 不会联网。

Grok Build 的用量限额面板可能只覆盖进程启动或恢复运行之后的活动；ProfileDeck 报告则读取所选日期内本地会话历史中的已完成回合。因此，即使两者都来自同一会话，总量也可能不同。

这两种金额都不是发票、credits 余额、配额或实际账单费用。同步或生成报告时，ProfileDeck 不会连接 xAI 或计费 API。

## 隐私范围

用量存储不包含原始提示词、代理结果、API 密钥、直接会话标识或完整源文件路径。ProfileDeck 不会上传用量数据，也不会将其用于遥测。存储与备份建议见 [本地数据与安全](../reference/data-security.md)。
