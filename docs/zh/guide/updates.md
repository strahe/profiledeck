# 更新桌面端

更新方式取决于安装方式：

| 安装方式 | 更新方法 |
| --- | --- |
| macOS 应用或 Linux 便携版桌面端 | 应用内更新 |
| Linux DEB 或 RPM | 从 [Releases](https://github.com/strahe/profiledeck/releases) 安装新版软件包 |
| 从源码构建的 CLI 或本地开发桌面端 | 手动重新构建或安装 |

## 应用内更新（macOS 与 Linux 便携版）

正式版通道只接收正式版本；Beta 通道接收 Beta 和正式版本。首次运行时按当前构建选择通道，以后安装会保留你的选择。自动检查默认开启，启动时和持续运行期间约每六小时检查一次。更新准备好后，由你决定何时重启。

重启前，ProfileDeck 会验证下载内容并创建加密应用备份。准备失败时，当前版本保持不变。Linux 便携版的安装目录必须可写；如果无法安全替换程序，请手动下载新版便携包。

## 更新 Linux 软件包

从 [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) 下载对应新版软件包并安装：

```bash
sudo apt install ./ProfileDeck_<version>_linux_amd64.deb
# 或
sudo dnf install ./ProfileDeck_<version>_linux_amd64.rpm
```

DEB 和 RPM 不使用应用内更新。不要混用软件包安装与便携版更新。安装要求见[快速开始](./getting-started.md)。
