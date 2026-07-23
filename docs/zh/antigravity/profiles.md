# Antigravity Profile

ProfileDeck 可以保存和切换存储在操作系统凭据存储中的 Antigravity 个人 OAuth 登录，但不会代替你登录 Antigravity。

## 开始前准备

1. 登录 Antigravity，并确认可以正常使用。
2. 启动 ProfileDeck；如果使用 CLI，请运行 `profiledeck init`。

ProfileDeck 不支持旧版 Antigravity 存储方式。

## 在桌面端保存 Profile

1. 在 ProfileDeck 侧边栏中打开 **Antigravity**。
2. 选择**保存当前登录**。
3. 输入创建后不会改变的 Profile ID 和用于显示的名称，然后选择**保存 Profile**。

新 Profile 会成为 Antigravity 的当前 Profile。ProfileDeck 不会显示其中的访问令牌或刷新令牌。

## 使用 CLI 保存 Profile

先检查当前登录，再保存它：

```bash
profiledeck antigravity detect
profiledeck antigravity profile create work --name Work
```

`detect` 只报告登录是否就绪，不会输出登录内容。创建命令要求当前登录有效。

查看或重命名已保存的 Profile：

```bash
profiledeck antigravity profile list
profiledeck antigravity profile show work
profiledeck antigravity profile update work --name "Work account"
```

## 切换 Profile

条件允许时，请先关闭 Antigravity 再切换，避免它在变更期间刷新登录；切换后重新打开即可。

在桌面端打开目标 Profile，选择**使用 Profile**，检查变更并确认。

使用 CLI 时，先预览再应用同一变更：

```bash
profiledeck plan antigravity work
profiledeck switch antigravity work --yes
```

ProfileDeck 会再次检查当前登录，并在变更前创建私有操作恢复点。如果切换中断，请使用[诊断与恢复](../operations/recovery.md)。

## 保存刷新的登录

Antigravity 运行时可能刷新登录。短期访问令牌的到期时间不能代表已保存 Profile 的可复用期限，因此 ProfileDeck 不会把它显示为登录过期时间。切离当前 Profile 时，ProfileDeck 会保存有效的刷新后登录。你也可以主动保存：

```bash
profiledeck antigravity profile save-current
```

在桌面端打开当前 Profile，然后选择**从当前 Antigravity 更新**。

## 删除 Profile

在桌面端打开 Profile 的操作菜单并选择**删除 Profile**，或运行：

```bash
profiledeck antigravity profile delete work --yes
```

这会从所有 Agent 中删除完整的全局 Profile，而不只是 Antigravity 数据。只有该 Profile 使用的已保存登录会删除，共享登录会保留。当前 Profile 或存在未完成操作的 Profile 不能删除。系统凭据存储中的当前 Antigravity 登录不会改变。

## 检查使用限额

桌面端会在启动时检查一次当前 Antigravity Profile，并在成功切换后检查一次新 Profile。需要再次检查时，请在当前 Profile 上选择**刷新限额**。ProfileDeck 不会在后台轮询。

这些检查会将当前 Profile 的访问令牌发送到 Google Cloud Code 服务。该服务契约未公开，使用它可能带来账号风险。检查期间，ProfileDeck 不会刷新、保存或回写令牌。

Profile 列表显示紧凑摘要；Profile 详情显示各分组的 5 小时和每周窗口、剩余百分比、重置时间与检查时间。非当前 Profile 可以保留本次应用会话中较早检查的快照，但必须先使用该 Profile，才能刷新结果。

限额快照只存在于内存，不会写入用量报告、导出、备份或 ProfileDeck 数据库，也不能用来判断此前的 Antigravity 活动属于哪个 Profile。

## 不支持的范围

ProfileDeck 不管理 Antigravity 登录流程、设置、旧版存储迁移、Manager 数据、模型级限额明细、用量归属，也不管理 SSH 或容器会话使用的独立登录文件。Antigravity 限额检查仅在桌面端提供。
