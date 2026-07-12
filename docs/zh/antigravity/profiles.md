# Antigravity Profile

ProfileDeck 支持 Antigravity agy v2 使用的 consumer OAuth 登录。Legacy Antigravity 存储和其他 Antigravity 版本不受支持。

## 保存当前登录

先通过 Antigravity agy v2 登录，然后运行：

```bash
profiledeck antigravity detect
profiledeck antigravity profile create work --name Work
```

`detect` 只报告 `valid`、`missing`、`invalid` 或 `unavailable`，不会打印登录内容。`profile create` 要求当前登录有效，将其保存为隐藏凭据，并把新 Profile 设为当前 Profile。

Desktop 侧栏中的 Antigravity Agent 提供相同流程。

## 查看和编辑 Profile

```bash
profiledeck antigravity profile list
profiledeck antigravity profile show work
profiledeck antigravity profile update work --name "Work account"
```

List 和 show 只显示 Profile 详情、登录过期时间、引用数和警告，不会包含 access token 或 refresh token。

## 切换 Profile

```bash
profiledeck plan antigravity work
profiledeck switch antigravity work --yes
```

Antigravity plan 只显示安全目标标签和 `create`、`update` 或 `noop`。系统凭据位置、登录 payload、预览和登录哈希始终隐藏。

ProfileDeck 会先创建私有备份，再更新系统凭据存储；写入前还会立即重新检查当前值。系统凭据存储不提供跨进程 compare-and-swap，因此 Antigravity 仍可能在写入前的最后窗口刷新登录。条件允许时，请先关闭 Antigravity，切换后再重启。

## 保存刷新后的登录

Antigravity 运行时可能刷新当前登录。切换时，ProfileDeck 会把有效的刷新结果保存到之前的当前 Profile。也可以显式运行：

```bash
profiledeck antigravity profile save-current
```

## 支持范围

Antigravity 支持不包括 OAuth 登录、legacy storage、Manager 导入、配额读取、用量归属或其他 Antigravity 版本。
