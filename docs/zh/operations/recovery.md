# 诊断、备份与恢复

如果 Profile 切换被阻止或没有完成，请打开**诊断**。如果需要保护或恢复 ProfileDeck 保存的应用数据，请打开**设置 → 备份**。这是两个独立流程。

## 先检查未完成切换

任何未解决的切换都会阻止新的 Profile 切换和应用数据恢复。请先在“诊断”中恢复或安全关闭该操作；ProfileDeck 不会为了执行新操作而丢弃已有恢复点。

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

## 重试恢复文件清理

ProfileDeck 通常会在切换、恢复或应用恢复后删除临时操作恢复文件。如果清理未能完成，“诊断”会显示**临时恢复文件需要清理**。已保存数据、诊断和应用备份仍可使用，但 Profile 切换和应用恢复会暂停，直至清理成功。

在“诊断”中选择**重试清理**，或运行：

```bash
profiledeck doctor retry-cleanup --yes
```

清理只会移除不属于未解决切换的临时操作恢复文件，不会改变任何工具的登录信息或设置。如果重试提示另一个操作正在进行，请关闭其他 ProfileDeck 窗口。如果警告仍然存在，请继续保护好数据目录，并在解决提示的文件系统问题后重试。

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

新版 ProfileDeck 需要更新现有本地数据时，会先检查数据并创建加密自动备份。如果检查或备份创建失败，ProfileDeck 会在更新数据前停止；如果后续数据更新失败，ProfileDeck 会停止启动并保留该加密备份供恢复。请关闭其他 ProfileDeck 窗口后重试；如果仍无法使用本地数据，请从桌面端恢复页面恢复一份确认正常的应用备份。

自动备份默认开启。最新自动备份超过 24 小时时，桌面端和托盘会补做一次；更新重启前、健康数据库恢复前和本地数据更新前也会创建。各类自动备份合计最多保留 10 份，其中本地数据更新前备份最多保留 3 份；手动备份由你自行删除。

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

应用恢复提交后，ProfileDeck 会删除已过期的操作恢复文件。如果清理未能完成，恢复后的数据仍然有效，桌面端会在重启时自动重试。在清理成功前，Profile 切换和再次恢复应用数据会保持暂停。

如果 ProfileDeck 启动时无法打开数据库，桌面端恢复页面仍可导入恢复密钥、列出可用备份并恢复。
