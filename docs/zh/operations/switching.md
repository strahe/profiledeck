# 审核并切换 Profile

切换会修改所选工具正在使用的登录或设置。写入前，ProfileDeck 会检查当前状态，在支持时保存即将切离的 Profile 的有效更新；只有切换成功，新 Profile 才会成为当前 Profile。预览会隐藏敏感值。

## 使用 CLI 切换

```bash
profiledeck-cli switch codex work --dry-run
profiledeck-cli switch codex work --yes
```

预览是可选的。切换其他工具时，替换 `codex` 和 `work`。如果要求实际切换与先前预览的状态一致，请在 `--yes` 命令中添加 `--plan-fingerprint <fingerprint>`；状态变化后请重新预览。

如果无法检查当前状态或创建恢复点，ProfileDeck 会在写入前停止。切换中断或被阻止时，运行 `profiledeck-cli doctor`，并按[诊断与恢复](./recovery.md)处理。未完成切换的恢复文件可能包含登录内容，请保护[本地数据目录](../reference/data-security.md)。

成功切换不能撤销；需要其他设置时，请切换到相应 Profile。已在运行的工具可能需要新建会话才能使用更改后的登录。
