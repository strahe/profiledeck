# Codex 托管配置

托管配置模式只修改 `$CODEX_HOME/config.toml` 中的部分 key：

- `model`
- `model_provider`
- `openai_base_url`

当你只需要切换 model 或 base URL，而不需要捕获 Codex auth 时，可以使用这种模式。

## 创建或更新 managed profile

```bash
profiledeck codex profile set work --model gpt-5.3-codex
```

省略 `model_provider` 时默认使用 `openai`：

```bash
profiledeck codex profile set work \
  --model gpt-5.3-codex \
  --model-provider openai
```

设置自定义 OpenAI-compatible base URL：

```bash
profiledeck codex profile set relay \
  --model gpt-5.3-codex \
  --openai-base-url https://api.example.com/v1
```

## 期望状态语义

`profile set` 会写入 ProfileDeck 托管 key 的完整期望状态。如果省略 `--openai-base-url`，ProfileDeck 会在 switch 时删除它托管的 `openai_base_url`。

非托管 Codex config key 和 section 会被保留，但 TOML 注释和顺序可能会变化，因为文件会被解析后重新编码。

## 绑定已有账号

可以把 managed config profile 绑定到已保存的 Codex account：

```bash
profiledeck codex profile set work-fast \
  --model gpt-5.3-codex \
  --account work
```

account 必须先通过 `codex profile capture` 或 `codex account import` 创建。

## 应用

```bash
profiledeck plan codex work-fast
profiledeck switch codex work-fast --yes
```

当 profile 包含 auth target 时，switch 也会写入 `$CODEX_HOME/auth.json`。
