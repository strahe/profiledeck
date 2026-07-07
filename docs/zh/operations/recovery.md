# 恢复操作

ProfileDeck 会记录 switch 和 rollback operation，因此中断的写入可以被检查和恢复。

## 诊断

```bash
profiledeck doctor
profiledeck doctor --json
```

`doctor` 报告：

- 数据库初始化和 schema 健康状况
- 待处理 (pending) 和失败的操作
- 切换锁 (switch lock) 状态
- 失效锁 (stale lock) 是否可修复
- 敏感路径权限警告

## 修复 stale lock

```bash
profiledeck doctor repair-lock --yes
```

只有明确 stale 的 lock file 才能修复。如果 lock owner 仍然看起来活跃，或 lock 无法被验证，repair 会被拒绝。

## 恢复失败 switch

```bash
profiledeck recover <switch-operation-id> --yes
```

recover 使用失败 switch operation 的 backup checkpoint。它用于不完整的 switch，不是常规撤销操作。

## 回滚已应用 switch

```bash
profiledeck backup list
profiledeck backup show <backup-id>
profiledeck rollback <backup-id> --yes
```

rollback 从 backup 恢复目标文件，并同步更新 ProfileDeck 当前激活状态 (active state) 和操作历史。恢复前也会为当前状态创建新的备份。
