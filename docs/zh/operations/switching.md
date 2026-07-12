# 切换流程

切换是修改已配置工具所用外部登录或设置的正常方式。

## 预览

```bash
profiledeck plan codex work
profiledeck plan codex work --json
```

预览是只读操作。文件目标会显示：

- 可能发生变化的每个文件；
- 文件将被创建、更新、保持不变，还是无法修改；
- 选择该操作的原因；
- 变更前后的 SHA-256 哈希；
- 隐藏敏感值的预览；
- 需要审核的警告；
- 用于精确应用已审核内容的 plan fingerprint。

Codex 登录内容始终隐藏。完整的已保存登录和 Config Set 数据不会出现在预览中。

Antigravity 系统凭据等敏感目标只显示安全标签、`create`、`update` 或 `noop` 以及操作原因。系统凭据位置、payload、预览和内容哈希始终隐藏。

Fingerprint 代表已审核的 Profile 和当前目标状态。如果相关目标或已保存的 Profile 在预览后发生变化，ProfileDeck 会在写入前拒绝该 fingerprint。

## 应用

```bash
profiledeck switch codex work --yes
profiledeck switch antigravity work --yes
```

要确保应用内容与之前的预览完全一致，请传入 fingerprint：

```bash
profiledeck switch codex work \
  --plan-fingerprint <fingerprint> \
  --yes
```

## ProfileDeck 如何保护切换

修改外部目标前，ProfileDeck 会：

1. 检查是否还有其他 ProfileDeck 更改正在进行；
2. 重新检查当前外部目标和已审核的切换内容；
3. 保留当前 Codex 登录和设置中的有效更改；
4. 创建备份；
5. 只修改需要更新的目标；
6. 外部更新成功后，才把所选 Profile 记录为当前 Profile。

ProfileDeck 无法安全确认目标、备份或已审核状态时，会停止且不应用切换。缺失、无效、符号链接或不支持的目标会显示警告或阻止操作，不会被静默保存。

切换开始后被中断或失败时，诊断页面会保留记录；只有存在可用备份时才会提供恢复操作。

## 备份

每次成功切换都会在 ProfileDeck 数据目录中保存备份。文件目标条目会显示路径、操作、哈希和权限，但不会打印敏感内容。

Codex 备份可能包含之前的 `auth.json` 和 `config.toml` 内容，请按敏感数据保护备份目录。

Antigravity 备份可能在私有 payload 文件中包含之前的登录；ProfileDeck 会在 POSIX 系统上以 `0600` 权限写入该文件。公共备份摘要不会显示其位置或哈希。

回滚和恢复会还原外部目标以及之前选择的 Profile。已经保存到 Profile 登录或 Config Set 中的更改仍会保留。
