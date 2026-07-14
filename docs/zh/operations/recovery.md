# 诊断与恢复

如果切换或回滚没有完成、Profile 切换被阻止，或 ProfileDeck 报告本地数据问题，请打开“诊断”。

## 先检查问题

在桌面端打开**诊断**，查看建议的处理方式。

使用 CLI 时运行：

```bash
profiledeck doctor
profiledeck doctor --json
```

诊断会检查 ProfileDeck 能否读取本地数据、是否有失败或未完成的操作、是否可能仍有其他变更正在运行，以及敏感本地文件是否保持私有。

## 恢复 Profile 切换

如果诊断确认没有变更仍在运行，并提示可以恢复切换，请使用桌面端提供的操作，或运行：

```bash
profiledeck doctor repair-lock --yes
```

不要仅因为切换耗时较长就运行此命令。如果无法安全确认当前情况，ProfileDeck 会拒绝操作。

## 恢复失败的切换

桌面端诊断显示失败切换存在可用备份时，请选择**恢复**并确认。

使用 CLI 时，请先运行 `profiledeck doctor`。对于显示为可恢复的失败切换，把该切换对应的标识符代入：

```bash
profiledeck recover <failed-switch-id> --yes
```

恢复会使用该次切换前创建的备份，还原文件或登录以及之前的当前 Profile。Codex、Claude Code 和 Antigravity 都支持此操作。如果切换已经成功，请使用回滚，而不是恢复。

## 撤销成功的切换

列出并检查备份：

```bash
profiledeck backup list
profiledeck backup show <backup-id>
```

然后恢复所需备份：

```bash
profiledeck rollback <backup-id> --yes
```

Codex、Claude Code 和 Antigravity 都支持回滚。恢复旧状态前，ProfileDeck 会先为当前状态再创建一个备份。回滚成功后，所选工具和创建该备份时的当前 Profile 都会恢复。

## 选择正确的操作

| 当前情况 | 应采取的操作 |
| --- | --- |
| 诊断提示切换被阻止，但没有变更正在运行 | 恢复 Profile 切换 |
| 失败切换存在可用备份 | 在诊断中选择**恢复**，或运行 CLI 恢复命令 |
| 切换已完成，但希望回到之前状态 | 回滚对应备份 |

如果诊断没有提供安全操作，或不存在可用备份，请勿手动删除 ProfileDeck 数据或备份。保留 ProfileDeck 数据目录，并先检查报告的错误再重试。
