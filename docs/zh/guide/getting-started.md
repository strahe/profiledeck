# 快速开始

ProfileDeck 当前是 Go CLI。命令示例默认 `profiledeck` 可执行文件已经在 `PATH` 中。

## 从源码编译

```bash
make build
```

编译产物写入 `bin/profiledeck`。

命令示例统一使用 `profiledeck` 作为可执行文件名。从源码 checkout 运行时，请先安装该二进制，或把 `bin/` 加入 shell path。

## 开发快捷命令

| 命令 | 用途 |
| --- | --- |
| `make fmt` | 使用 `go fmt ./...` 格式化 Go package。 |
| `make vet` | 运行 `go vet ./...`。 |
| `make test` | 运行 `go test ./...`。 |
| `make build` | 从 `cmd/profiledeck` 编译 `bin/profiledeck`。 |
| `make check` | 依次运行 format、vet、test 和 build。 |
| `make clean` | 删除本地构建产物。 |

文档站使用 npm 脚本：

```bash
cd docs
npm install
npm run dev
npm run build
npm run preview
```

## 初始化 ProfileDeck

```bash
profiledeck init
profiledeck status
```

`init` 会创建 ProfileDeck 运行目录和 SQLite 应用数据库。默认运行根目录是：

```text
<os-user-config-dir>/profiledeck
```

应用数据库位于：

```text
<os-user-config-dir>/profiledeck/profiledeck.db
```

使用 `--config-dir` 可以把运行目录放到另一个用户配置目录下：

```bash
profiledeck --config-dir /tmp/profiledeck-dev init
```

在 POSIX 系统上，ProfileDeck 会尽力为 runtime、backup、export、log 和 lock 目录设置较严格的权限。

## 第一个 Codex profile

```bash
profiledeck codex detect
profiledeck codex profile capture work
profiledeck plan codex work
profiledeck switch codex work --yes
```

`codex profile capture` 读取当前 Codex home。解析顺序是：

1. `--codex-dir`
2. `CODEX_HOME`
3. `~/.codex`

完整账号切换要求 Codex home 中存在 `$CODEX_HOME/auth.json`。
