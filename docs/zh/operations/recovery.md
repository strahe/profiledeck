# 诊断、备份与恢复

如果 Profile 切换被阻止或没有完成，请打开**诊断**。如果需要保护或恢复 ProfileDeck 保存的应用数据，请打开**设置 → 备份**。这是两个独立流程。

## 先检查未完成切换

桌面端“诊断”只显示尚未解决的根切换操作，并为每项显示当前可安全执行的操作。

使用 CLI 时运行：

```bash
profiledeck doctor
profiledeck doctor --json
```

如果诊断确认没有变更仍在运行，并提示可以修复切换锁，请使用桌面端提供的操作，或运行：

```bash
profiledeck doctor repair-lock --yes
```

不要仅因为切换耗时较长就修复锁。切换锁仍被持有，或 ProfileDeck 无法安全识别所有受影响目标时，恢复会被拒绝。

## 处理未完成切换

诊断会提供以下两种操作之一：

- **关闭未完成记录**：ProfileDeck 已确认没有目标被修改，或所有目标已经处于切换前状态。
- **恢复切换前状态**：所有目标仍处于切换前状态或本次切换的目标状态。

确认桌面端提供的操作，或使用 `doctor` 显示的操作 ID：

```bash
profiledeck recover <operation-id> --yes
```

恢复可能会还原工具自己的文件或所选系统登录，随后恢复此前的当前 Profile 记录。如果目标被其他程序修改、恢复数据损坏或目标无法读取，ProfileDeck 会拒绝写入并提示需要检查的内容。失败后仍可针对原始切换重试。

成功切换不会保留恢复文件，也不能撤销。如果希望使用其他配置，请选择目标 Profile 后重新切换。

## 创建和管理应用备份

应用备份包含完整的 ProfileDeck 数据库，包括已保存 Profile、设置、用量记录和数据库中保存的凭据。它不包含外部工具的工作文件，也不包含操作系统凭据存储中的条目。

使用以下命令创建和检查备份：

```bash
profiledeck backup create
profiledeck backup list
profiledeck backup show <backup-id>
profiledeck backup export <backup-id> --output <私有文件>
```

自动备份默认开启。最新自动备份超过 24 小时时，桌面端和托盘会补做一次；更新重启前和健康数据库恢复前也会创建。定时、更新前和恢复前备份合计保留最近 10 个；手动备份由你自行删除。

备份文件使用系统凭据存储中的恢复密钥加密。把备份移到其他电脑前，请单独导出密钥：

```bash
profiledeck backup key status
profiledeck backup key export --output <私有密钥文件> --yes
profiledeck backup key import --file <私有密钥文件> --yes
```

请妥善保护导出的密钥。导入不同密钥时必须传入 `--replace --yes`；除非重新导入旧密钥，否则新密钥无法打开旧密钥加密的备份。

## 恢复应用数据

可以恢复已管理或已导出的备份：

```bash
profiledeck backup restore <backup-id> --yes
profiledeck backup restore --file <私有文件> --yes
```

ProfileDeck 会先验证加密归档和数据库，再替换当前应用数据。当前数据库健康时，会先创建自动安全备份；当前数据库损坏时，确认后可以跳过安全备份继续恢复。

恢复会清空所有当前 Profile 标记，并关闭未解决操作，避免把历史状态误认为外部工具的当前状态。它不会修改工具自己的文件或系统登录，也不会自动应用任何 Profile。桌面端成功后会自动重启；CLI 恢复后请重启 ProfileDeck，并明确切换到需要的 Profile。桌面端或其他 ProfileDeck 进程正在使用应用数据时，CLI 会拒绝恢复。

如果 ProfileDeck 启动时无法打开数据库，桌面端恢复页面仍可导入恢复密钥、列出可用备份并恢复。
