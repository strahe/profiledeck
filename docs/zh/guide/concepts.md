# Profile、登录与设置

Profile 用来标记某个受支持工具应使用的登录和设置。保存 Profile 不会立即修改对应工具；只有确认切换或恢复未完成切换后，ProfileDeck 才会修改工具文件或系统登录。

## Profile 保存什么

| 工具 | 保存的登录 | 保存的设置 |
| --- | --- | --- |
| Codex | 一份 Codex 登录 | 一组已保存的 Codex 设置（配置集） |
| Claude Code | 一份官方订阅登录 | 不包含设置 |
| Antigravity | 一份个人 OAuth 登录 | 不包含设置 |

每个 Profile 都有用于 CLI 命令和链接的永久 ID。不同工具共用同一个 Profile ID 命名空间，一个 Profile 也可以包含多个工具的已保存数据。

## 当前 Profile

ProfileDeck 会分别记录每个受支持工具的当前 Profile。当前 Profile 对应该工具正在使用的登录或文件：

- Codex 使用当前 Codex 目录中的 `auth.json` 和 `config.toml`。
- Claude Code 在 macOS 上使用 Keychain 中的官方订阅登录，在 Linux 和 Windows 上使用凭据文件。
- Antigravity 使用系统凭据存储中的当前登录。

离开当前 Profile 前，ProfileDeck 会在可以安全保存时保留有效的刷新登录或 Codex 设置。内容缺失、无效或不受支持时，ProfileDeck 会报告问题，不会静默保存。

## 已保存登录

一份登录可以由多个 Profile 共享。更新共享登录会影响所有使用它的 Profile，因此桌面端会在保存前显示受影响的 Profile 数量。

ProfileDeck 可能显示 Codex Account ID 的末尾字符，帮助区分不同登录。这个值只用于显示，不会决定更新或共享哪份登录。

## Codex 配置集

配置集是一组可复用的 Codex 设置，内容来自用户级 `config.toml`。第一个 Codex Profile 会创建名为 `shared` 的配置集；后续 Profile 可以复用它，也可以保存独立副本。

多个 Profile 共享配置集时，保存更改后的 Codex 设置会同时更新这些 Profile。如果某个 Profile 的设置需要独立变化，请复制配置集。只有未被任何 Profile 使用的配置集才能删除。

配置集不包含会话、日志、Skills、插件缓存、项目 `.codex/config.toml` 或系统策略。

## ProfileDeck 会修改什么

创建、编辑、Fork 或导入 Profile 时，只会更改 ProfileDeck 保存的数据。确认切换或恢复未完成切换后，ProfileDeck 才可能修改所选工具正在使用的登录或文件。

每次修改前，ProfileDeck 都会根据工具当前状态重新检查并创建临时操作恢复点。正常流程请参阅[审核并切换](../operations/switching.md)；操作未完成时请参阅[诊断与恢复](../operations/recovery.md)。成功切换不能撤销。

## 本地数据

Profile、配置集、已保存登录、偏好设置、用量报告和操作历史都保存在 ProfileDeck 本地数据目录中。受支持工具仍然拥有自己正在使用的文件和系统凭据条目。

已保存数据、操作恢复点和加密应用备份都可能包含完整登录数据。复制、导出或删除 ProfileDeck 数据前，请阅读[数据与安全](../reference/data-security.md)。
