# frp-console

便携式 frp / 饥荒管理面板：单个可执行文件，双击即用。frp 0.71 自带 Web dashboard，
本工具补上它没有的部分——实例生命周期、配置向导、`verify` 校验、日志、状态；
同一个面板也管理 DST 服务器进程（执行 bat、日志、多世界配置）。

> 分步操作（建实例、启停、dashboard、升级、排障）见 **[USAGE.md](USAGE.md)**。

## 特性

- **单 exe、零安装**：首次运行在 exe 同级生成 `conf/ profiles/ logs/ bin/`。
- **自带 frp 二进制**：构建时按平台把 frpc/frps gzip 进 exe，首次运行释放到 `bin/`；
  已存在则跳过，方便手动替换版本。
- **实例管理**：创建 / 启动 / 停止 / 重启 / 删除，配置与会话数据全部留在数据目录。
- **可视化参数编辑**：表单创建常用参数；配置页直接编辑 TOML，保存前跑
  `frpc|frps verify`，通过后写盘并对运行中的 frpc 热重载（`/api/reload`）。
- **一键 dashboard**：打开 frp 原生面板（`http://127.0.0.1:<webPort>/static/`），
  代理表单、流量、客户端列表都在那里。
- **日志 / 状态**：捕获子进程输出（去掉 ANSI 颜色），frpc 走 `/api/status`，
  frps 走 `/api/v2/proxies`。
- **Windows 无控制台**：GUI 子系统构建，启动后只有托盘图标，不弹黑框。
- **实例进程无窗口**：frpc/frps 与 DST 分片均以 `CREATE_NO_WINDOW` 启动，
  输出只写日志文件，启停不会闪黑框。
- **DST 服务器**：只需指定集群目录，自动识别 `Master` / `Caves`；配置只读写
  常用项，不改文件结构与其它键；启动时用 steamcmd 目录自动定位 Dedicated Server，
  设置 `SteamAppId`/工作目录后直接拉起分片（无控制台窗口），停止时发送
  `c_shutdown()`；日志可看启动输出或各分片 `server_log.txt`。
- **steamcmd**：DST 页顶部设置目录；可一键 `app_update 343050 validate`，
  异步等待并显示 `Success!` 与实时日志。
- **单实例**：重复双击不会起第二个进程，只会打开已在运行的面板。
- **可退出**：托盘菜单“退出”、面板右上角“退出”、`POST /api/quit` 都走同一条
  关闭流程（停止全部实例 → 关面板）。
- 面板只监听 `127.0.0.1`。

## 数据布局

```text
dstlauncher(.exe)          # 唯一需要分发的文件
conf/panel.json            # 监听地址 / 是否自动开浏览器
bin/frpc, bin/frps         # 首次运行从 exe 内释放；可手工替换
profiles/<id>/
    meta.json
    frpc.toml | frps.toml  # 真实配置，可直接改
    frpc.store.json        # frpc 的 store（dashboard 表单编辑代理时生成）
dst/<id>/meta.json         # 指向外部集群目录 + 生成的 launchArgs（不复制集群文件）
logs/<id>.log              # frp 子进程 stdout/stderr
logs/dst-<id>.log          # DST 子进程 stdout/stderr
logs/panel.log             # 面板自身日志（Windows GUI 构建唯一启动日志）
```

## 构建

```bash
# 开发构建（不嵌入 frp，读取 bin/ 或 PATH）
go build -o dstlauncher .

# 发布构建：linux/amd64 + windows/amd64 + linux/armv7l
./build.sh
# 产物：dist/dstlauncher_<os>_<arch>/dstlauncher(.exe)
#      armv7l 目录为 dist/dstlauncher_linux_armv7l/
```

`build.sh` 会按 `FRP_VERSION`（默认 0.71.1）下载对应平台的 frp 发布包，gzip 后
嵌入 exe；解压后的缓存位于 `dist/.cache/frp_<ver>_<os>_<arch>/`。缓存已存在时不再联网。
首次构建还需要 Go 模块网络（拉取托盘依赖），或提前 `GOOS=windows go mod download`。

- 直连 GitHub 失败时自动尝试 `FRP_MIRROR`（默认 `https://ghfast.top/`，置空可关闭）。
- 完全离线：把官方发布包（Windows `.zip`，其它平台 `.tar.gz`）解压到
  `dist/.cache/frp_<ver>_<os>_<arch>/` 再执行构建。
- macOS：先把 `frp_0.71.1_darwin_arm64.zip` 放进缓存，再在 `TARGETS` 里加
  `"darwin arm64"`。

## 发布（GitHub Actions）

`.github/workflows/build.yml`：

- push `main`：`go vet` + `go test` → 构建 linux/windows → 上传 artifacts；
- push `v*` tag：额外创建/更新 GitHub Release，附
  `dstlauncher-<ver>-windows-amd64.exe`、`dstlauncher-<ver>-linux-amd64`。
- release 任务需要 `contents: write`（工作流已声明），使用内置 `GITHUB_TOKEN`。

裁剪版本：

```bash
git tag v0.5.0
git push origin v0.5.0
```

约 2-4 分钟后在 Actions 和 Releases 里能看到结果。

## 运行

```bash
./dstlauncher                  # 数据目录 = exe 所在目录；日志同时写 logs/panel.log
./dstlauncher -listen 127.0.0.1:7600 -no-open
```

Windows：双击 `dstlauncher.exe`，**不弹控制台**，直接在托盘出现图标并打开浏览器：

- 托盘右键：打开面板 / 打开数据目录 / 退出。
- 再次双击 exe：只打开已在运行的面板，不会起第二个进程。
- 启动日志写在 `logs/panel.log`（GUI 程序没有控制台可看）。
- 调试需要控制台时：`WINDOWS_CONSOLE=1 ./build.sh`。

参数：

| 参数 | 说明 |
|---|---|
| `-data <dir>` | 覆盖数据目录（开发调试用） |
| `-listen <addr>` | 覆盖 `conf/panel.json` 的监听地址 |
| `-no-open` | 不自动打开浏览器 |
| `-version` | 打印版本 |

环境变量 `FRP_CONSOLE_HOME` 等价于 `-data`。

## 安全

- 面板无登录，绑定 `127.0.0.1`；**不要**反代到公网。
- dashboard 使用配置里的 Basic Auth（默认用户 `admin`，密码在创建时生成，
  可在配置页 TOML 中查看/修改）。
- `webServer`、`store.path` 均只写本机回环地址。
- `POST /api/quit` 无鉴权但仅回环可达，等价于“停止全部实例并退出”。

## 架构

```text
main.go                便携布局、HTTP 启动、浏览器打开
internal/app           路径/设置/原子写/二进制查找
internal/store         profiles/<id>/meta.json
internal/frpx          TOML 生成、verify、dashboard 认证解析、admin API
internal/dst           DST 集群识别、INI 读写、steamcmd/服务端定位、启动参数生成
internal/unit          子进程监管（日志、状态、优雅停止→强杀、进程树终止）
internal/web           JSON API + 内嵌静态页
internal/embedfs       构建时嵌入的 frp 二进制（gzip）
tray_windows.go        Windows 托盘菜单（仅 Windows 编译）
notify_windows.go      GUI 构建下的致命错误对话框
web/                   单页 UI（无构建步骤）
```

`internal/unit` 是后续 DST 面板要复用的部分：一个 Unit = 命令 + 工作目录 +
日志 + 停止策略；frp 只是一个 adapter，DST 的 master/caves 同理。

## 当前边界（P0 框架）

已实现：frp 与 DST 两类实例、创建/启停/重启/删除、TOML 编辑 + 校验、热重载、
日志、状态、dashboard 跳转、DST 集群接入 / 自动开服 / 常用配置读写、单文件分发、
Windows 托盘 / 单实例 / 实例进程无窗口 / 统一退出。

未实现（按需再加）：开机自启、实例级自动重启/退避、端口占用预检、
导入已有 toml、DST 控制台指令、远程 agent、面板登录。
