# 本地数据与安全

ProfileDeck 会在你的设备上保存 Profile、登录、设置、用量报告、备份和操作历史。请将其数据目录视为敏感内容。

## 查找数据目录

默认位置是：

```text
<用户配置目录>/profiledeck
```

常见位置如下：

| 系统 | 默认位置 |
| --- | --- |
| macOS | `~/Library/Application Support/profiledeck` |
| Linux | `$XDG_CONFIG_HOME/profiledeck` 或 `~/.config/profiledeck` |
| Windows | `%AppData%\profiledeck` |

如果传入 `--config-dir <directory>`，ProfileDeck 会改用 `<directory>/profiledeck`。

该目录包含 `profiledeck.db`、加密应用备份，以及未完成切换所需的临时恢复数据。ProfileDeck 可能在数据库或操作恢复数据中保存 Codex、Claude Code 和 Antigravity 登录，以便安全切换 Profile。

## 保护本地数据

ProfileDeck 使用 age X25519 加密 `.profiledeck-backup` 文件。当前数据库和未完成切换的恢复数据不会单独加密，因此能读取你本地文件的人仍可能读取已保存的登录。操作系统允许时，ProfileDeck 会限制这些文件的权限。

- 启用操作系统的全盘加密和屏幕锁定。
- 不要同步、提交、上传或分享完整的 ProfileDeck 数据目录。
- 移动或重新安装 ProfileDeck 时，请使用已导出的加密应用备份，并单独导出恢复密钥。
- 恢复密钥文件不得放入仓库或共享文件夹。

Claude Code 支持与 Claude Desktop 相互独立。ProfileDeck 不会读取或更改 Claude Desktop 的登录、设置或进程。

## 了解应用备份与操作恢复

应用备份包含完整的 ProfileDeck 数据库，并会在发布到 `backups/` 前加密。ProfileDeck 把现有本地数据更新为新版格式前也会先创建加密备份。如果检查或备份创建失败，ProfileDeck 会在更新数据前停止；如果后续数据更新失败，启动会停止，并在恢复页面保留该加密备份。

手动备份由你自行删除。自动备份每 24 小时执行一次，并会在更新重启、数据库恢复或本地数据更新前创建。各类自动备份合计最多保留 10 份，其中本地数据更新前备份最多保留 3 份。

私有 X25519 恢复密钥保存在操作系统凭据存储中，不会写入备份。把备份移到其他系统前，请单独导出密钥；替换当前密钥也不会重新加密已有备份。

切换修改外部工具前，ProfileDeck 会在 `recovery/<operation-id>/` 下创建私有恢复点。其中可能包含未经过应用备份加密的完整 Codex 文件、Claude Code 订阅登录或 Antigravity 登录。恢复点只为未完成切换保留，成功后会删除；它不会出现在备份列表中，不能导出，也不能用于撤销成功切换。

操作状态正式生效前，ProfileDeck 会先登记清理责任；只有恢复目录完成同步后才会清除该责任。因此，崩溃或文件系统错误可能使已完成操作的恢复数据仍然存在，并显示清理警告。警告存在时，Profile 切换和应用恢复会暂停，但读取、诊断和应用备份仍可使用。请运行 `profiledeck doctor retry-cleanup --yes`，或在桌面端“诊断”中选择**重试清理**。清理不会改变工具登录信息或设置。

备份列表和预览只显示安全元数据。作为纵深保护，请保持加密备份私有，并且绝不分享操作恢复数据。

## 安全导出 Codex Profile

`profiledeck codex profile export` 会创建明确标记为敏感的备份，其中包含所选 Profile 的完整 Codex 登录和已保存设置。获得该文件的人可能可以使用对应账号。

请选择仓库和共享文件夹之外的私有位置。不要提交或分享该导出文件，也不要把它放在准备删除的 ProfileDeck 数据目录中。

导入会先检查文件并报告冲突，再保存任何内容。导入不会把 Profile 设为当前 Profile，不会更改 Codex 文件，也不会启用自动限额刷新和登录续期。

导出与导入命令见 [Codex Profile](../codex/profiles.md#备份与恢复-profile)。

## 了解何时联网

ProfileDeck 的大部分操作只使用本地数据。

- 用量同步和报告读取本地 Codex 会话文件，不会请求计费服务。
- Codex 限额查询会使用所选的已保存登录连接 Codex 或 OpenAI。该登录绝不会发送到已保存 Codex 设置中的自定义模型服务地址。限额结果是临时数据，不会写入用量报告。
- Antigravity 限额检查会把当前 Antigravity 访问令牌发送到用于检查的固定 Google Cloud Code 服务。检查过程中，ProfileDeck 不会刷新或保存令牌。结果只保留在应用内存中，不会写入数据库、用量报告、导出或备份。
- 桌面端更新检查和下载会连接 GitHub 上公开的 ProfileDeck Release。

ProfileDeck 不提供云同步，也不会发送遥测或分析数据。Codex 自动限额刷新和登录续期默认关闭，而且只会在桌面端打开或驻留菜单栏时运行。

## 输出和用量报告不会包含什么

普通预览、命令、日志、错误和备份摘要会隐藏已保存登录及其他敏感设置。导出应用备份只会原样复制密文。只有你主动创建的敏感 Codex Profile 导出文件会在其私有归档中暴露完整登录和设置。

用量报告会保存令牌数、模型名称、时间信息和成本估算，但不会保存原始提示词、原始回复、API 密钥、完整会话记录或完整源文件路径。本地 Codex 活动无法可靠判断请求由哪个 Profile 或 ChatGPT 账号处理，因此 ProfileDeck 不会猜测此类归属。
