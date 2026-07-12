# 恢复操作

切换或回滚没有完成、Profile 切换被阻止，或本地数据需要处理时，请使用诊断功能。

## 检查诊断信息

```bash
profiledeck doctor
profiledeck doctor --json
```

诊断会检查：

- ProfileDeck 是否可以读取本地数据；
- 未完成或失败的更改；
- 是否可能还有其他 ProfileDeck 更改正在运行；
- 敏感本地文件是否仅当前用户可访问。

Desktop 会用面向用户的语言显示相同问题，并且只在可以安全继续时提供操作。

## 恢复 Profile 切换功能

```bash
profiledeck doctor repair-lock --yes
```

只有诊断明确表示可以安全恢复时才使用此命令。其他更改可能仍在进行，或当前情况无法确认时，ProfileDeck 会拒绝执行。

## 恢复失败的切换

```bash
profiledeck recover <switch-operation-id> --yes
```

恢复会使用失败切换前保存的备份。它适用于被中断或失败的切换，不是常规撤销操作。

## 回滚成功的切换

```bash
profiledeck backup list
profiledeck backup show <backup-id>
profiledeck rollback <backup-id> --yes
```

回滚会从备份中还原文件和所选 Profile。恢复旧状态前，ProfileDeck 也会先备份当前文件。
