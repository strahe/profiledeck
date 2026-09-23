# 诊断、备份与恢复

切换被阻止或中断时，运行 `profiledeck-cli doctor`。未完成切换得到处理前，不能开始新切换或恢复应用数据。

## 处理未完成切换

```bash
profiledeck-cli doctor
profiledeck-cli recover <operation-id> --yes
```

使用“诊断”给出的操作 ID 和处理方式。恢复可能还原工具正在使用的文件或系统登录，但不会改变当前 Profile。如果目标已被其他程序修改，或无法安全检查，恢复会在写入前停止。解决报告的问题后，可以重试原操作。

只有“诊断”确认没有切换正在运行并提供修复操作时，才运行 `profiledeck-cli doctor repair-lock --yes`。如果显示**临时恢复文件需要清理**，运行 `profiledeck-cli doctor retry-cleanup --yes`。清理不会更改工具登录或设置；成功清理前，切换和应用恢复仍不可用。

成功切换不能恢复或撤销。

## 备份 ProfileDeck 数据

应用备份是完整 ProfileDeck 数据库的加密副本，包含已保存的 Profile、设置、用量和数据库中的凭据，不包含工具正在使用的文件或系统凭据存储条目。

```bash
profiledeck-cli backup create
profiledeck-cli backup list
profiledeck-cli backup export <backup-id> --output <私有文件>
```

自动备份默认开启，桌面端或托盘运行期间约每天执行一次。更新、健康数据库恢复和本地数据升级前还会创建备份；自动备份最多保留十份。手动备份由你自行删除。

恢复密钥单独保存在系统凭据存储中；把备份移到其他电脑前，请另外导出密钥：

```bash
profiledeck-cli backup key export --output <私有密钥文件> --yes
profiledeck-cli backup key import --file <私有密钥文件> --yes
```

请保护密钥文件。用 `--replace --yes` 更换当前密钥不会重新加密旧备份；要打开旧备份，必须重新导入旧密钥。

## 恢复应用数据

```bash
profiledeck-cli backup restore <backup-id> --yes
profiledeck-cli backup restore --file <私有文件> --yes
```

恢复前会验证备份；当前数据库健康时，还会先创建安全备份；数据库损坏时，经确认可跳过该备份继续恢复。

恢复会清除当前 Profile 标记并关闭未完成操作，不会修改工具正在使用的文件或系统登录，也不会自动应用已保存 Profile。

CLI 恢复后请重启 ProfileDeck，再明确切换到需要的 Profile。通过 CLI 恢复前，先关闭其他 ProfileDeck 进程。

如果启动时无法打开数据库，桌面端仍可恢复备份。如果提示本地数据格式不受支持，请恢复兼容的备份。若要改用新的本地数据，请关闭 ProfileDeck，将整个[数据目录](../reference/data-security.md)移到私密位置，再重新打开。保留原目录以备检查。
