# 更新桌面端

如何更新取决于你如何安装 ProfileDeck：

- **macOS 应用**或 **Linux 便携版桌面端**：可在应用内检查更新。
- **Linux DEB 或 RPM**：从 GitHub Releases 下载并安装新版软件包。应用内不会检查或下载更新。
- **CLI** 与本地 **dev** 桌面端构建：不检查在线更新，也不显示更新通道选择器。

## 应用内更新（macOS 与 Linux 便携版）

ProfileDeck 提供两个持久保存的更新通道：

- **正式版**只接收正式版本（`X.Y.Z`）。
- **Beta**接收 Beta 版本（`X.Y.Z-beta.N`）和正式版本，因此可以从 Beta 升级到同版本或更高版本随后发布的正式版。

首次运行时，正式构建默认选择“正式版”，Beta 构建默认选择“Beta”。后续安装会保留你的选择；从 Beta 升级到正式版后也不会自动退出 Beta 通道。

### 检查更新

自动检查默认开启。ProfileDeck 会在启动后检查一次，并在保持打开或在后台运行（例如托盘）期间每 6 小时检查一次。

打开**设置 → 常规 → 应用更新**，可以选择更新通道、开启或关闭自动检查、立即检查，或查看下载进度。下载期间 ProfileDeck 会保持打开；发现更新后，侧栏也会显示下载和准备状态。

更新器处于空闲、已是最新版本或错误状态时可以切换通道。正在检查、下载或已有待重启更新时，需要先完成当前流程。自动检查开启时，切换通道后会立即按新通道检查。

### 安装已下载的更新

更新准备好后，在方便时选择左下角侧栏中的**重启并更新**。在你选择此操作前，ProfileDeck 会保持打开。

重启前，ProfileDeck 会验证更新并创建加密的自动应用备份。如果验证、备份或准备失败，ProfileDeck 不会安装更新，当前版本也会保持不变。更新前备份与其他自动备份合计保留最近 10 个。稍后返回**设置 → 常规 → 应用更新**重试。

在 Linux 便携版中，ProfileDeck 还会在创建备份和重启前，确认下载的更新可以安全移入可执行文件所在目录。如果当前安装无法安全替换，ProfileDeck 会保留现有版本，并关闭该安装的应用内更新；请改为手动下载新版便携包。见[便携版桌面端](./getting-started.md#便携版桌面端)。

## 更新 Linux 软件包

DEB 和 RPM 安装不使用应用内更新。**设置 → 常规 → 应用更新**会说明需要从 GitHub Releases 安装新版软件包。

从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载对应新版软件包，然后安装：

```bash
sudo apt install ./ProfileDeck_<version>_linux_amd64.deb
# 或
sudo dnf install ./ProfileDeck_<version>_linux_amd64.rpm
```

不要混用 DEB、RPM 安装与便携版应用内更新。若某版本有问题，请安装之后发布的修复版本。

见[安装 DEB 或 RPM](./getting-started.md#安装-deb-或-rpm)。

## 了解下载文件

GitHub Releases 提供：

- 用于 macOS 安装的公证 Universal DMG
- 用于 macOS 应用内更新的公证 Universal ZIP
- 用于 Linux 便携版安装与更新的已签名单文件 tar 压缩包
- 用于安装 Linux 桌面端与 CLI 的 DEB 和 RPM

应用自行更新时，只会下载 Wails 签名更新清单选中的平台产物，并在准备更新前校验 SHA-512 摘要与 Ed25519ph 签名。

发布说明保存在对应的 [GitHub Release](https://github.com/strahe/profiledeck/releases) 中。
