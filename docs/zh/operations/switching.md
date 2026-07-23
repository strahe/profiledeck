# 审核并切换 Profile

切换会改变 Codex、Claude Code 或 Antigravity 使用的登录或设置。ProfileDeck 会让你先检查变更，并在应用前创建临时恢复点。

## 在桌面端切换

1. 打开 **Codex**、**Claude Code** 或 **Antigravity**。
2. 选择要使用的 Profile。
3. 选择**使用 Profile**。
4. 检查将要变更的文件或登录，以及所有警告。
5. 确认切换。

只有变更成功后，ProfileDeck 才会把该 Profile 标记为当前 Profile。如果所选工具没有立即使用新登录，请重启工具或新建会话。

## 使用 CLI 预览

切换前先运行 `plan`：

```bash
profiledeck plan codex work
profiledeck plan claude-code personal
profiledeck plan antigravity work
```

如需结构化输出，可添加 `--json`：

```bash
profiledeck plan codex work --json
```

对于文件，预览会显示路径将被创建、更新还是保持不变。对于已保存的登录，预览只显示安全的目标名称和操作。所有预览都会隐藏敏感登录内容。

警告会说明文件或登录是否缺失、无效、不受支持或无法安全变更。请先处理阻止切换的警告。

## 使用 CLI 应用

```bash
profiledeck switch codex work --yes
profiledeck switch claude-code personal --yes
profiledeck switch antigravity work --yes
```

如需确保应用的状态与之前检查的内容完全一致，请复制 `plan` 返回的指纹：

```bash
profiledeck switch codex work \
  --plan-fingerprint <fingerprint> \
  --yes
```

如果预览后 Profile 或所选工具发生变化，ProfileDeck 会拒绝该指纹，不写入任何内容。请重新运行 `plan` 并检查新结果。

## 切换期间会发生什么

更改所选工具前，ProfileDeck 会：

1. 检查是否还有其他 ProfileDeck 变更正在运行；
2. 再次检查当前文件或登录；
3. 在支持时保存即将切离的 Profile 中的有效更新；
4. 创建私有操作恢复点；
5. 只更改必要的文件或登录；
6. 所有变更成功后，再把新 Profile 标记为当前 Profile。

如果无法确认当前状态或创建可用恢复点，ProfileDeck 会停止且不应用切换。中断或失败的操作会保留在“诊断”中，以便安全恢复。切换成功后，ProfileDeck 会删除恢复点。

## 妥善保护恢复数据

未完成切换的恢复点可能包含之前的 Codex 文件、Claude Code 订阅登录或 Antigravity 登录。请保持 ProfileDeck 数据目录私有，不要提交、上传或分享其中的恢复文件。

恢复会把未完成切换影响的目标还原到切换前状态，但不会改变当前 Profile。如果未完成切换后当前 Profile 已发生变化，ProfileDeck 会在写入前拒绝恢复。已经保存到 Profile 中的更新仍会保留。成功切换不能撤销；如需更换配置，请切换到目标 Profile。可用操作见[诊断、备份与恢复](./recovery.md)。
