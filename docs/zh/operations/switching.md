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
- 资源绑定与脱敏工作副本捕获摘要
- 计划指纹 (plan fingerprint)

敏感值会被脱敏。Codex auth preview 始终整体脱敏，credential 或 Config Set 的 raw payload 不会进入捕获摘要。

对 Codex 来说，fingerprint 同时覆盖源与目标绑定、目标文件 hash，以及等待捕获的有效工作副本 hash。因此工作副本变化会使之前审阅的 plan 失效。

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
3. 根据当前数据库绑定和目标文件重建 plan，并暂存有效 Codex 工作副本捕获。
4. 校验目标文件 hash。
5. 创建 backup checkpoint。
6. 再次校验 hash。
7. 原子写入发生变化的文件。
8. 在同一个数据库事务中提交捕获、active state 和 applied operation。

Codex 绑定不变时，有效工作副本会被捕获但不会重写文件。绑定变化时，会先暂存旧资源的有效工作副本，再写入目标资源。如果工作副本已经与目标资源一致，不会把它误存到旧绑定。缺失、无效、symlink 或非普通文件不会被静默存储；plan 会根据风险给出警告或阻止切换。

如果操作失败，ProfileDeck 会保留失败操作记录 (failed operation record)，供 `doctor` 和 `recover` 使用。

## 备份

每次成功应用的 switch 都会在 runtime backup 目录保存 backup manifest。manifest 包含路径、action、hash、mode 和相对备份条目。常规命令输出不会包含 raw desired content。

Codex 备份可能包含之前的 `auth.json` 和 `config.toml` 内容。backup 目录应按敏感数据处理。

Rollback 和 recovery 会恢复目标文件及之前的 active state，但不会撤销已由 applied switch 捕获的有效 credential 或 Config Set 状态。
