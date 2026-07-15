# 更新桌面端

桌面端更新适用于 macOS 14 或更高版本的 macOS arm64 Alpha 分发版本。CLI 不会自行更新。

## 检查更新

自动检查默认开启。ProfileDeck 会在启动后检查一次，并在保持打开或隐藏到菜单栏期间每 6 小时检查一次。

打开**设置 → 应用更新**，可以开启或关闭自动检查、立即检查，或查看下载进度。下载期间 ProfileDeck 会保持打开。

## 安装已下载的更新

更新准备好后，选择**立即重启**进行安装。选择**稍后**可以继续工作；未经确认，ProfileDeck 不会自行重启。

重启前，ProfileDeck 会验证更新并创建加密的自动应用备份。如果验证、备份或准备失败，ProfileDeck 不会安装更新，当前版本也会保持不变。更新前备份与其他自动备份合计保留最近 10 个。稍后返回**设置 → 应用更新**重试。

## 首次打开 Alpha

当前 Alpha 尚未通过 Apple 公证，因此 macOS 可能阻止首次启动。如果文件来自 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 且你信任该文件，请打开**系统设置 → 隐私与安全性**，对 ProfileDeck 选择**仍要打开**。

只有首次打开下载的 Alpha 时需要执行这一步。
