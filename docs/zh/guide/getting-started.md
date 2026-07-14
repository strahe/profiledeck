# 快速开始

ProfileDeck 提供 Go CLI 和 macOS 桌面应用。命令示例默认 `profiledeck` 可执行文件已经在 `PATH` 中。

## 从源码编译

```bash
make build
```

编译产物写入 `bin/profiledeck`。

命令示例统一使用 `profiledeck` 作为可执行文件名。从源码 checkout 运行时，请先安装该二进制，或把 `bin/` 加入 shell path。

## 开发快捷命令

| 命令 | 用途 |
| --- | --- |
| `make fmt` | 使用 gofumpt 和 gci 格式化全部 Go package。 |
| `make lint` | 运行只读的 Go 格式与静态分析检查。 |
| `make test` | 运行 `go test ./...`。 |
| `make build` | 从 `cmd/profiledeck` 编译 `bin/profiledeck`。 |
| `make core-check` | 运行 CLI/core lint、测试和构建。 |
| `make desktop-check` | 运行 Wails 边界、bindings、前端、构建和 Desktop 测试。 |
| `make docs-check` | 安装文档依赖并构建文档站。 |
| `make check` | 运行完整的 core、Desktop 和文档门禁。 |
| `make clean` | 删除本地构建产物。 |

`make check` 不会改写 tracked 源码或生成的 bindings。需要更新这些文件时，请显式运行 `make fmt` 或 `make desktop-bindings`。完整验证要求在 macOS 上运行，且 PATH 中存在兼容的 `golangci-lint` v2 和 `wails3`；其他平台使用 `make core-check` 运行可移植的 CLI/core 门禁。

文档任务也统一使用 Make target：

```bash
make docs-install
make docs-dev
make docs-build
make docs-preview
```

## 初始化 ProfileDeck

```bash
profiledeck init
profiledeck status
```

`init` 会创建 ProfileDeck 本地数据和备份目录。默认数据目录是：

```text
<os-user-config-dir>/profiledeck
```

应用数据库位于：

```text
<os-user-config-dir>/profiledeck/profiledeck.db
```

使用 `--config-dir` 可以把 ProfileDeck 数据放到另一个用户配置目录下：

```bash
profiledeck --config-dir /tmp/profiledeck-dev init
```

在 POSIX 系统上，ProfileDeck 会尽力限制本地数据和备份目录的访问权限。

分发版 macOS arm64 Alpha 会在 ProfileDeck 运行期间检查更新。可在[桌面端更新](/zh/guide/updates)了解如何管理自动检查和安装已下载的更新。

## 第一个 Codex profile

```bash
profiledeck codex detect
profiledeck codex profile create work
profiledeck codex profile list
profiledeck plan codex work
profiledeck switch codex work --yes
```

`codex profile create` 读取当前 Codex home，并要求 `config.toml` 与 `auth.json` 同时存在。解析顺序是：

1. `--codex-dir`
2. `CODEX_HOME`
3. `~/.codex`

Codex profile 切换要求 Codex home 中存在 `$CODEX_HOME/auth.json`。

第一个 Profile 会创建并激活名为 `shared` 的 Config Set。后续 Profile 默认复用 active Config Set，因此新增登录状态通常只需先登录对应账号，再次执行 `codex profile create`。如果当前 `config.toml` 应成为独立 Config Set，使用 `--new-config-set <id>`。

## 第一个 Antigravity Profile

先通过 Antigravity agy v2 登录，然后运行：

```bash
profiledeck antigravity detect
profiledeck antigravity profile create work
profiledeck plan antigravity work
profiledeck switch antigravity work --yes
```

ProfileDeck 只支持 agy v2 consumer OAuth 登录，不会发起 OAuth 登录，也不会导入 Antigravity Manager 数据。
