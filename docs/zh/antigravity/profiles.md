# Antigravity Profile

ProfileDeck 保存并切换操作系统凭据存储中的 Antigravity 个人 OAuth 登录，不代替你登录，也不管理旧版存储、设置或 SSH 与容器使用的独立登录。

## 开始前准备

登录 Antigravity，并确认可以正常使用。CLI 用户需要运行一次 `profiledeck-cli init`。用 `detect` 检查当前登录是否受支持，再创建 Profile：

```bash
profiledeck-cli antigravity detect
profiledeck-cli antigravity profile create work
```

第一个 Profile 会成为当前 Profile。要保存另一个登录，先在 Antigravity 中登录该账号，再创建另一个 Profile。

## 切换和保存刷新后的登录

条件允许时，先关闭 Antigravity 再切换，避免它在变更期间刷新登录。切换命令见[审核并切换](../operations/switching.md)。

Antigravity 运行时可能刷新登录。切离当前 Profile 时，ProfileDeck 会保存有效的刷新后登录。若要在登录其他账号前保存，可运行：

```bash
profiledeck-cli antigravity profile save-current
```

短期访问令牌的到期时间不能代表已保存 Profile 的可复用期限。共享和删除的影响见[Profile 与设置](../guide/concepts.md)。

## 检查使用限额

桌面端在启动和切换后检查当前 Profile，之后需要手动检查。检查会把访问令牌发送到未公开的 Google Cloud Code 服务，可能带来账号风险。检查期间，ProfileDeck 不会刷新或回写令牌。

限额结果仅保留在内存中，不会写入用量报告或备份，也不能判断此前活动属于哪个 Profile。限额检查只在桌面端提供。
