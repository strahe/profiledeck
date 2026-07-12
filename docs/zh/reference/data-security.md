# 数据与安全

ProfileDeck 管理本地文件和受支持工具的隐藏登录数据。请把数据目录和备份按敏感数据保护。

## 本地数据

默认数据目录是：

```text
<os-user-config-dir>/profiledeck
```

其中包含应用数据库、备份、导出文件和运行所需文件。`--config-dir` 会改变此位置使用的用户配置目录。

`profiledeck.db` 保存 Profiles、Config Sets、Codex 与 Antigravity 登录、设置、用量报告和操作历史。为了恢复和切换 Profile，数据库可能保存完整的 Codex `auth.json`、`config.toml` 和 Antigravity agy v2 登录 payload。

数据库不会做静态加密。ProfileDeck 会在 POSIX 系统上尽力限制文件权限，但能够读取本地文件的人也可能读取已保存的登录数据。

当前账号限额信息只是临时显示，不会写入数据库。

## 备份

切换和回滚备份可能包含之前的工具文件。Codex 备份可能包含完整的 `auth.json` 和 `config.toml` 内容。

对于文件目标，备份命令会显示文件名、操作、哈希和权限，但不会打印敏感文件内容。请保护好备份目录。

Antigravity 切换备份可能在私有 payload 文件中包含之前的登录。公共 plan 和备份摘要不会显示系统凭据位置、payload 或 payload 哈希。

## 敏感 Profile 导出

`profiledeck codex profile export` 会创建敏感本地备份。JSON 文件包含完整的 Codex 登录数据和设置。获得文件的人可能可以访问你的账号。

ProfileDeck 要求显式指定输出路径，拒绝符号链接目标，并在 POSIX 系统上以 `0600` 权限写入文件。它不会创建或修改用户选择的父目录。

导入会先检查备份并报告冲突，再执行更改。向已有全局 Profile 附加 Codex 绑定时，它会保留该 Profile 的名称和描述。它不会把 Profile 设为当前、恢复自动更新设置，也不会写入 Codex 文件。导入后的 Profile 默认关闭自动刷新限额和登录续期。

请把导出备份放在准备删除的 ProfileDeck 数据目录之外。不要提交或分享这些文件。

## 脱敏

ProfileDeck 会在预览、常规命令输出、日志、错误和结果摘要中隐藏敏感值。Codex 登录预览始终完全隐藏。

以下命令只显示摘要，不会打印完整的已保存登录 payload 或 Config Set 内容：

```bash
profiledeck codex profile list
profiledeck codex profile show <profile-id>
profiledeck codex config-set list
profiledeck codex config-set show <config-set-id>
profiledeck plan codex <profile-id>
profiledeck antigravity profile list
profiledeck antigravity profile show <profile-id>
profiledeck plan antigravity <profile-id>
profiledeck backup show <backup-id>
profiledeck doctor
```

导出和导入命令的输出也只有摘要。只有用户明确选择的备份文件包含完整登录数据和设置。

## 限额检查与登录续期

检查 Profile 限额时，ProfileDeck 会使用该 Profile 已保存的登录连接 Codex 或 OpenAI。Profile 自定义的 model-provider URL 不会收到已保存的 ChatGPT 登录 Token。

自动刷新限额和登录续期默认关闭，并且只在 ProfileDeck 打开或隐藏到托盘时运行。Desktop 还会在启动时检查一次当前 Profile；页面导航不会重复检查。

受支持的托管登录可能会在检查限额时续期。部分外部登录方式可以提供限额，但无法自动续期。如果 Codex 自动更新不可用，手动检查仍可能以不修改已保存登录的方式工作。

限额信息只在 ProfileDeck 运行期间保留。限额检查不会显示或记录原始登录 Token 或临时文件位置。

## 用量功能不会保存的数据

用量报告会保存 Token 数、成本估算、模型名称和时间信息。ProfileDeck 不会把原始 Codex session 记录、prompt、completion、API key 或完整来源路径保存为用量数据。

用量报告只包含聚合结果。本地 Codex 活动无法可靠识别实际请求来自哪个 Profile 或 ChatGPT 账号，因此 ProfileDeck 不会猜测或发布账号级用量。

Config Set 不包含 skills、plugin 缓存、项目 `.codex/config.toml` 或系统策略。

ProfileDeck 只接受 Antigravity agy v2 consumer OAuth payload，不会执行 Antigravity OAuth 登录、导入 Manager 数据，也不会使用外部账号字段做匹配。
