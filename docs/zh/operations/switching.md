# 切换流程

switch 是正常情况下唯一会写入目标工具文件的路径。

## 预览

```bash
profiledeck plan codex work
profiledeck plan codex work --json
```

plan 是只读操作，包含：

- 目标路径 (target path)
- 操作行为 (action)：`create`、`update`、`noop` 或 `unsupported`
- 状态原因 (status reason)
- 变更前后的 SHA-256 哈希值 (before/desired hash)
- 脱敏预览 (redacted preview)
- 计划指纹 (plan fingerprint)

敏感值会被脱敏。Codex auth preview 始终整体脱敏。

## 应用

```bash
profiledeck switch codex work --yes
```

如果希望严格应用之前看到的 plan，可以传入 fingerprint：

```bash
profiledeck switch codex work \
  --plan-fingerprint <fingerprint> \
  --yes
```

如果锁内重建出的 plan 与预期 fingerprint 不一致，switch 会在写文件前失败。

## 安全流程

`switch` 执行以下步骤：

1. 创建待处理操作记录 (pending operation record)。
2. 获取 ProfileDeck switch lock。
3. 根据当前数据库和目标文件重建 plan。
4. 校验目标文件 hash。
5. 创建 backup checkpoint。
6. 再次校验 hash。
7. 原子写入发生变化的文件。
8. 更新当前激活状态 (active state)，并将操作标记为已应用 (applied)。

如果操作失败，ProfileDeck 会保留失败操作记录 (failed operation record)，供 `doctor` 和 `recover` 使用。

## 备份

每次成功应用的 switch 都会在 runtime backup 目录保存 backup manifest。manifest 包含路径、action、hash、mode 和相对备份条目。常规命令输出不会包含 raw desired content。

Codex 备份可能包含之前的 `auth.json` 内容。backup 目录应按敏感数据处理。
