# Antigravity Profile

ProfileDeck 可以保存和切换 Antigravity agy v2 使用的个人 OAuth 登录，但不会代替你登录 Antigravity。

## 开始前准备

1. 使用 Antigravity agy v2。
2. 在 Antigravity 中完成登录，并确认登录可用。
3. 启动 ProfileDeck；如果使用 CLI，请运行 `profiledeck init`。

ProfileDeck 不支持旧版 Antigravity 存储方式或其他 Antigravity 版本。

## 在桌面端保存 Profile

1. 在 ProfileDeck 侧边栏中打开 **Antigravity**。
2. 选择**保存当前登录**。
3. 输入创建后不会改变的 Profile ID 和用于显示的名称，然后选择**保存 Profile**。

新 Profile 会成为 Antigravity 的当前 Profile。ProfileDeck 不会显示其中的访问令牌或刷新令牌。

## 使用 CLI 保存 Profile

先检查当前登录，再保存它：

```bash
profiledeck antigravity detect
profiledeck antigravity profile create work --name Work
```

`detect` 只报告登录是否就绪，不会输出登录内容。创建命令要求当前登录有效。

查看或重命名已保存的 Profile：

```bash
profiledeck antigravity profile list
profiledeck antigravity profile show work
profiledeck antigravity profile update work --name "Work account"
```

## 切换 Profile

条件允许时，请先关闭 Antigravity 再切换，避免它在变更期间刷新登录；切换后重新打开即可。

在桌面端打开目标 Profile，选择**使用 Profile**，检查变更并确认。

使用 CLI 时，先预览再应用同一变更：

```bash
profiledeck plan antigravity work
profiledeck switch antigravity work --yes
```

ProfileDeck 会再次检查当前登录，并在变更前创建私有备份。如果切换中断，请使用[诊断与恢复](../operations/recovery.md)。

## 保存刷新的登录

Antigravity 运行时可能刷新登录。切离当前 Profile 时，ProfileDeck 会保存有效的刷新后登录。你也可以主动保存：

```bash
profiledeck antigravity profile save-current
```

在桌面端打开当前 Profile，然后选择**从当前 Antigravity 更新**。

## 不支持的范围

ProfileDeck 不提供 Antigravity 登录、旧版存储迁移、Manager 导入、限额查询、用量归属，也不支持 agy v2 以外的 Antigravity 版本。
