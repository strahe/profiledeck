# Profile、登录与设置

Profile 保存登录和设置。一个 Profile 可以包含多个工具的数据，但每个工具分别记录自己的当前 Profile。创建或编辑 Profile 只改变 ProfileDeck 保存的数据；[切换](../operations/switching.md)才会修改所选工具正在使用的登录或文件。

Profile ID 创建后不能修改，并在各工具之间共用。一份已保存登录可被多个 Profile 共用；更新它会影响所有使用它的 Profile。保存前，ProfileDeck 会显示受影响的数量。

## 配置集

Codex 和 Grok Build 还会把用户级 `config.toml` 设置保存为配置集，两种工具的配置集互不共用。第一个 Profile 使用 `shared`；必要时根据当前设置创建。之后的 Profile 可以复用或另存一份。

修改共享配置集会影响所有使用它的 Profile。需要独立修改时请复制。配置集不包含会话、日志、插件、项目设置或系统策略。

## 删除 Profile

```bash
profiledeck-cli profile delete <profile-id> --yes
```

删除会从所有工具中移除完整的 Profile，以及只有它使用的已保存登录和配置集；共享数据保留。当前 Profile 或仍被未完成切换引用的 Profile 不能删除。删除不会退出工具登录，也不会更改工具正在使用的文件。

## 本地数据

ProfileDeck 在本地保存 Profile、登录、设置、用量报告和备份。当前数据库和未完成切换的恢复数据可能包含完整登录内容。复制或分享这些文件前，请阅读[数据与安全](../reference/data-security.md)。
