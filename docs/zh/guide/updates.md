# 桌面端更新

桌面端更新适用于 macOS 14 或更高版本的 macOS arm64 Alpha。CLI 不会自行更新。

## 检查更新

自动检查默认开启。ProfileDeck 会在启动后检查一次，并在保持运行期间每 6 小时检查。发现更新后，ProfileDeck 会在后台下载，不会自行关闭。

在**设置 → 应用更新**中可以开启或关闭自动检查、立即检查，或查看下载进度。

## 安装更新

更新准备好后，选择**立即重启**即可安装；选择**稍后**可以继续工作。未经确认，ProfileDeck 不会自行重启。

安装前，ProfileDeck 会备份本地数据。如果更新检查未通过或准备失败，ProfileDeck 不会安装它，当前版本也会保持不变。可稍后返回设置重试。

## 首次打开 Alpha

当前 Alpha 尚未通过 Apple 公证，因此 macOS 可能阻止首次启动。如果你从[ProfileDeck 官方 Releases 页面](https://github.com/strahe/profiledeck/releases)下载并信任该文件，可打开**系统设置 → 隐私与安全性**，对 ProfileDeck 选择**仍要打开**。

只有首次打开下载的 Alpha 时需要执行这一步。
