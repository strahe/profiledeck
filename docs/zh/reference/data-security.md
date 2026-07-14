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

该目录包含 ProfileDeck 保存的数据、切换与更新备份，以及完成或恢复更改所需的文件。ProfileDeck 可能在其中保存 Codex、Claude Code 和 Antigravity 登录，以便切换和恢复 Profile。

## 保护本地数据

ProfileDeck 不会为已保存数据和备份另行加密。操作系统允许时，ProfileDeck 会限制文件权限，但能读取你本地文件的人仍可能读取已保存的登录。

- 启用操作系统的全盘加密和屏幕锁定。
- 不要同步、提交、上传或分享 ProfileDeck 数据目录。
- 移动或重新安装 ProfileDeck 前，备份整个目录。
- 确认 Profile 可以正常切换后，再清理旧备份。

Claude Code 支持与 Claude Desktop 相互独立。ProfileDeck 不会读取或更改 Claude Desktop 的登录、设置或进程。

## 了解切换与更新备份

每次切换和回滚都会在更改所选工具前创建私有备份。备份可能包含完整 Codex 文件、Claude Code 订阅登录或 Antigravity 登录。

安装桌面端更新前，ProfileDeck 也会备份本地数据，并保留最近三个更新备份。如果更新验证或安装失败，当前版本仍可继续使用。

备份列表和预览会隐藏敏感内容，但备份文件本身仍须保持私有。

## 安全导出 Codex Profile

`profiledeck codex profile export` 会创建明确标记为敏感的备份，其中包含所选 Profile 的完整 Codex 登录和已保存设置。获得该文件的人可能可以使用对应账号。

请选择仓库和共享文件夹之外的私有位置。不要提交或分享该导出文件，也不要把它放在准备删除的 ProfileDeck 数据目录中。

导入会先检查文件并报告冲突，再保存任何内容。导入不会把 Profile 设为当前 Profile，不会更改 Codex 文件，也不会启用自动限额刷新和登录续期。

导出与导入命令见 [Codex Profile](../codex/profiles.md#备份与恢复-profile)。

## 了解何时联网

ProfileDeck 的大部分操作只使用本地数据。

- 用量同步和报告读取本地 Codex 会话文件，不会请求计费服务。
- Codex 限额查询会使用所选的已保存登录连接 Codex 或 OpenAI。该登录绝不会发送到已保存 Codex 设置中的自定义模型服务地址。限额结果是临时数据，不会写入用量报告。
- 桌面端更新检查和下载会连接 GitHub 上公开的 ProfileDeck Release。

ProfileDeck 不提供云同步，也不会发送遥测或分析数据。Codex 自动限额刷新和登录续期默认关闭，而且只会在桌面端打开或驻留菜单栏时运行。

## 输出和用量报告不会包含什么

普通预览、命令、日志、错误和备份摘要会隐藏已保存登录及其他敏感设置。只有你主动创建的敏感 Codex 导出文件包含完整的已导出登录和设置。

用量报告会保存令牌数、模型名称、时间信息和成本估算，但不会保存原始提示词、原始回复、API 密钥、完整会话记录或完整源文件路径。本地 Codex 活动无法可靠判断请求由哪个 Profile 或 ChatGPT 账号处理，因此 ProfileDeck 不会猜测此类归属。
