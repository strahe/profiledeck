# 本地数据与安全

ProfileDeck 在你的设备上保存 Profile、登录、设置、用量报告和备份。请将其数据目录视为敏感内容。

## 查找数据目录

| 系统 | 默认位置 |
| --- | --- |
| macOS | `~/Library/Application Support/profiledeck` |
| Linux | `$XDG_CONFIG_HOME/profiledeck` 或 `~/.config/profiledeck` |
| Windows | `%AppData%\profiledeck` |

`--config-dir <directory>` 改用 `<directory>/profiledeck`。其中包含 `profiledeck.db`、加密应用备份和未完成切换的恢复文件。

## 保护已保存数据

应用备份使用 age X25519 加密。当前数据库和未完成切换的恢复文件没有单独加密，可能包含完整登录或设置。不要同步、提交、上传或分享数据目录；请启用全盘加密和屏幕锁。`profiledeck-cli doctor` 可以报告允许其他本地用户访问的文件权限。

私有备份恢复密钥保存在系统凭据存储中，不在备份里。把备份移到其他电脑前，请单独导出密钥，并避免把密钥文件放在共享目录。命令见[备份与恢复](../operations/recovery.md)。成功切换不保留恢复点，也不能撤销。

## 何时联网

- 用量报告读取本地 Codex 和 Grok Build 会话。价格检查从 GitHub 下载公开费率，不上传本地用量。运行 `profiledeck-cli usage pricing auto off` 可关闭自动检查。
- ChatGPT Codex 限额检查使用所选登录连接 Codex 或 OpenAI。API Key 限额检查将已保存密钥发送到该 Profile 的自定义 Base URL；HTTP 不会加密传输中的密钥。
- Grok Build credits 检查使用已安装的 Grok Build，可能续期当前登录。
- Antigravity 限额检查将当前访问令牌发送到未公开的 Google Cloud Code 服务，可能带来账号风险；检查不会刷新或回写令牌。
- 桌面端更新检查和下载会连接 GitHub 上的 ProfileDeck Release。

限额和 credits 结果仅保留在内存中。ProfileDeck 不提供云同步，也不发送遥测。

## 输出和用量报告

预览、命令、日志、错误和备份摘要会隐藏已保存登录及敏感设置。导出的备份仍保持加密；单独导出的恢复密钥是敏感文件。

用量报告保存令牌数、模型、日期和估算，不保存原始提示词、回复、代理结果、API Key、完整会话记录或完整源文件路径。历史活动无法可靠归属到某个 Profile、已保存登录或账号。
