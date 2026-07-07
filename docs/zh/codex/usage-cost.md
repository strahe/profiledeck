# Codex 用量与成本

ProfileDeck 可以导入本地 Codex session 用量，并根据 token 数估算成本。

## 同步用量

```bash
profiledeck usage sync codex
```

默认扫描：

```text
$CODEX_HOME/sessions/**/*.jsonl
```

如果没有设置 `CODEX_HOME`，则回退到 `~/.codex`。使用 `--codex-dir` 可以指定 Codex home：

```bash
profiledeck usage sync codex --codex-dir /path/to/codex-home
```

importer 只保存派生出的用量事件 (usage events)，不保存 raw prompts、completions 或原始 JSONL events。

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
- 所有事件都能估价时的 USD 估算成本
- unknown cost event count

如果任一事件成本未知，JSON 输出中的 `estimated_cost_usd` 为 null，cost status 为 `unknown`。

## 估算限制

成本是基于二进制内静态模型价格表的本地估算。如果模型未知、价格缺失，或日志没有暴露足够计费上下文，ProfileDeck 会保留 token 数并把成本标记为 `unknown`。

ProfileDeck 不查询 provider billing API、余额或 relay 服务。
