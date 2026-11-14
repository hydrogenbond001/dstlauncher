# frp-console 操作手册

面向实际使用的分步操作。架构与构建说明见 [README.md](README.md)。

## 1. 启动

### Windows

界面：左侧导航切换 **frp 实例 / DST 服务器**，中间是实例列表，右侧显示配置、
日志与状态；DST 页顶部横栏配置 steamcmd 目录并更新服务端。

1. 把 `dstlauncher.exe` 放到任意目录（建议空目录，如 `D:\dstlauncher\`）。
2. 双击。**不会出现控制台窗口**，托盘出现图标，浏览器自动打开
   `http://127.0.0.1:7600/`。
3. 托盘右键菜单：打开面板 / 打开数据目录 / 退出。
4. 再次双击 exe 只会打开已运行的面板，不会起第二个进程。

启动异常时看 `logs\panel.log`（GUI 程序没有控制台可看）。

### Linux

```bash
./dstlauncher            # 控制台运行，日志同时写 logs/panel.log
./dstlauncher -no-open   # 不自动开浏览器
```

Ctrl+C 会停止所有实例并退出。

### 首次运行生成

```text
conf/panel.json            监听地址、是否自动开浏览器
bin/frpc, bin/frps         从 exe 内释放，可手工替换
profiles/<id>/             每个实例的配置与元数据
logs/<id>.log              实例输出
logs/panel.log             面板自身日志
```

面板默认只监听 `127.0.0.1:7600`；改端口编辑 `conf/panel.json` 的 `listen`，
或用 `-listen 127.0.0.1:7600` 临时覆盖。

## 2. 创建 frpc 实例（客户端）

“新建实例”表单，类型选 **frpc 客户端**：

| 字段 | 填写 |
|---|---|
| 名称 | 显示名，同时生成目录 id（重名自动加 `-2`） |
| serverAddr / serverPort | frps 的地址和 bindPort（默认 7000） |
| token | 与 frps 的 `auth.token` 一致；都没配就留空 |
| 面板端口 | 该实例的 dashboard 端口，默认 7400；多实例不能重复 |
| 面板密码 | dashboard 的 Basic Auth；默认随机生成，写入 TOML 明文 |
| 代理 | 点“+ 添加代理”，逐个填写 |

代理按类型填写：

| 类型 | 必填 | 说明 |
|---|---|---|
| tcp / udp | 名称、localPort、remotePort | remotePort 必须在 frps 的 `allowPorts` 内 |
| http / https | 名称、localPort、域名 | 多个域名逗号分隔；无域名可填 subDomain |
| stcp / xtcp / sudp | 名称、localPort、secretKey | 访问端需相同 secretKey |
| tcpmux | 名称、localPort、域名 | 同 http |

示例：把 Windows 上 DST 的 UDP 端口映射出去（取 frps `allowPorts` 内的两个）：

| 名称 | 类型 | localIP | localPort | remotePort |
|---|---|---|---|---|
| dst-master | udp | 127.0.0.1 | 10999 | 10999 |
| dst-caves | udp | 127.0.0.1 | 10998 | 10998 |

若还要映射 10888 这类端口，先确认它在 frps 的 `allowPorts` 内，否则会被拒绝。

点“创建”：服务端先跑 `frpc verify`，通过才落盘并打开详情页；失败不会留下半成品。

## 3. 创建 frps 实例（服务端）

类型选 **frps 服务端**：

| 字段 | 说明 |
|---|---|
| bindPort | 客户端接入端口，默认 7000 |
| token | 与客户端一致 |
| 面板端口 / 密码 | frps dashboard 与 API，默认 7500 |
| allowPorts | 允许客户端申请的 remotePort，如 `80,10000-12000`；留空不限制 |

> 本机已有一个系统级 frps 在跑（7000 端口、`/opt/frp_0.71.1_linux_amd64`）时，
> 面板里再建 frps 实例必须换 bindPort，或先停掉系统实例再由面板接管。

## 4. 创建 DST 服务器

左侧切到 **DST 服务器**。顶部横栏先填 **steamcmd 目录**（保存一次即可），
右侧是“新建 DST 服务器”。

| 字段 | 说明 |
|---|---|
| 名称 | 显示名 |
| 集群目录 | 指向 `...\DoNotStarveTogether\Cluster_2`（必须含 `cluster.ini`，内有 Master/Caves） |

世界自动识别：一级子目录里带 `server.ini` 即为一个分片。启动不再需要配置 bat。

### 定位游戏与启动

- 面板在 steamcmd 目录下的 `steamapps/common/*Starve*Dedicated Server` 中查找
  `dontstarve_dedicated_server_nullrenderer`，也兼容直接放在 steamcmd 目录内。
- 启动时按集群目录生成命令并保存到 `dst/<id>/meta.json` 的 `launchArgs`：
  `-persistent_storage_root <root> -conf_dir <conf> -cluster <目录名> -shard <Master|Caves> -console`。
  真实路径通常是 `...\Klei\DoNotStarveTogether\<账号ID>\Cluster_2`，
  其中 `<账号ID>` 由游戏自动插入，面板会跳过它（不会把它当成 `-conf_dir`）。
- 工作目录 = 游戏根目录，环境变量自动带 `SteamAppId=322330`、`SteamGameId=322330`；
  不依赖面板 exe 位置，移动程序目录不受影响。
- 启动前检查 `server_port` / `master_port` 的 UDP 占用，被占用会直接提示；
- Master 先启动，确认进程存活 2s 后再启动 Caves；
- 分片用 `CREATE_NO_WINDOW` 启动，**不弹黑框**；stdout/stderr 写入
  `logs/dst-<id>.log`（含 cwd/参数），游戏自身日志在各分片 `server_log.txt`。
- **停止**：向每个分片 stdin 发送 `c_shutdown()`，最多等 10s，未退再结束进程树。

详情“状态”里能看到实际执行的命令。

### steamcmd 更新服务端

DST 顶部横栏：

1. 填 steamcmd 目录（含 `steamcmd.exe` / `steamcmd.sh`），点“保存”；
2. 点“更新服务端”执行
   `+login anonymous +app_update 343050 validate +quit`；
3. 状态栏显示“更新中… Ns”，下方小日志实时滚动；出现 `Success!` 即成功。

更新可能较慢，steamcmd 有时会先自我更新——面板会一直等待到进程结束，
同一时间只允许一个更新任务。

### 配置（详情 → 配置）

只读取、只改常用键，不重建文件：

- `cluster.ini`：房间名、密码、描述、语言、游戏模式、最大玩家、PVP、空服暂停、
  `master_port`、`cluster_key`、`shard_enabled`、`console_enabled`；
- 各世界 `server.ini`：`server_port`、`is_master`、`master_server_port`、
  `authentication_port`；
- `cluster_token.txt`、`adminlist.txt`。

保存只替换这些键，注释、未知键、键顺序和 CRLF 换行保留。

## 5. 实例生命周期

| 按钮 | 行为 |
|---|---|
| 启动 | `frpc/frps -c profiles/<id>/<config>`，工作目录为该 profile |
| 停止 | frpc 先调 `/api/stop` 优雅退出，3s 未退再强杀；frps 直接终止 |
| 重启 | 停止后立即启动（改完配置最稳妥的方式） |
| 删除 | 先停进程，再删 `profiles/<id>/` 和 `logs/<id>.log`，需二次确认 |
| 配置 | 打开详情页（配置 / 日志 / 状态） |

启动后卡片绿点变亮，显示 pid 与 dashboard 端口；Windows 下不会弹控制台窗口。

## 6. 配置编辑

“配置”页是实例的真实 TOML 文件，表单覆盖不到的字段都在这里改。

1. 编辑文本。
2. 点“保存并校验”：先写临时文件跑 `frpc verify`（或 `frps verify`），
   失败则旧文件不动，错误原文显示在顶部横幅。
3. 校验通过后原子替换；若实例正在运行且是 frpc，自动调 `/api/reload` 热加载。
   frps 没有热重载接口，需要“重启”实例。

DST 的“配置”页不是原始文件编辑器，而是常用项表单：加载现有 `cluster.ini` /
`server.ini` / `cluster_token.txt` / `adminlist.txt`，保存时只替换表单对应的键，
其它内容原样保留（详见第 4 节）。

注意：frpc 有两份配置来源——这里的 TOML，和 dashboard 表单写入的
`frpc.store.json`。两者启动时合并；在 dashboard 里改代理不会出现在 TOML 中。

## 7. 使用 frp 原生 dashboard

实例详情右上角“打开 dashboard”，或直接访问：

```text
http://127.0.0.1:<面板端口>/static/
```

- 用户名/密码 = 创建时填的 `webServer.user/password`（TOML 里可见，默认 admin）。
- frpc dashboard 支持查看代理状态、改 TOML 并 Update & Reload、表单增删代理（写 store）。
- frps dashboard 可看客户端/代理/流量。

## 8. 日志与状态

- **日志**：frp 写 `logs/<id>.log`，DST 写 `logs/dst-<id>.log`；页面每 1.5s
  增量拉取，已去除 ANSI 颜色，保留最近 5000 行，自动滚到底部。
- **状态**：进程 running/pid/启动时间/最后错误；frpc 额外展示 `/api/status`
  （每个代理 running/error），frps 展示 `/api/v2/proxies`。
  面板端口或密码不对时显示 `adminError`。DST 只显示进程状态与工作目录/命令。

## 9. 退出与单实例

- 托盘“退出”、页面右上角“退出”、`POST /api/quit` 三处等价：
  停止所有实例 → 关闭面板 → 进程退出。
- 退出接口只绑定回环地址，不要反代到公网。
- Windows 关闭浏览器不会退出面板；需要托盘退出或页面退出。

## 10. 升级 frp 二进制

`bin/frpc(.exe)`、`bin/frps(.exe)` 首次运行从 exe 内解压；已存在则跳过。
要换版本：停掉相关实例 → 覆盖 `bin/` 下的文件 → 重新启动实例。
面板不会覆盖你手工放入的二进制。

## 11. 构建与打包

```bash
# 开发构建（不嵌入 frp，读 bin/ 或 PATH）
go build -o dstlauncher .

# 发布构建：linux/amd64 + windows/amd64
./build.sh

# 需要控制台窗口调试 Windows 版
WINDOWS_CONSOLE=1 ./build.sh

# 指定版本号
VERSION=0.2.1 ./build.sh
```

- 产物：`dist/dstlauncher_<os>_<arch>/dstlauncher(.exe)`。
- frp 发布包缓存在 `dist/.cache/frp_<ver>_<os>_<arch>/`，存在则不联网。
- 直连 GitHub 失败会自动尝试 `FRP_MIRROR`（默认 `https://ghfast.top/`）。
- 首次构建需要 Go 模块网络（托盘依赖）；离线时提前 `GOOS=windows go mod download`。
- macOS：把 `frp_0.71.1_darwin_arm64.zip` 解压进缓存，并在 `TARGETS` 加 `"darwin arm64"`。

## 12. 故障排查

| 现象 | 原因 / 处理 |
|---|---|
| Windows 启停实例弹黑框 | 0.2.0 及更早的 bug；升级 0.2.1。若仍出现，先结束残留的旧 `dstlauncher.exe`/`frpc.exe` |
| 双击后什么都没发生 | 看 `logs/panel.log`；若面板已在运行，第二次只打开已有面板，属正常 |
| DST 启动报“集群目录不存在/没有 cluster.ini” | 选到 `Cluster_2` 这一层，不要选 `DoNotStarveTogether` 或 `bin64` |
| 启动报“未设置 steamcmd 目录” | 在 DST 顶部横栏填目录并保存 |
| 启动报“未找到 Don't Starve Together Dedicated Server” | 先点“更新服务端”安装 343050，或确认游戏在 `<steamcmd>/steamapps/common` 下 |
| 更新状态长期“更新中” | steamcmd 可能在校验/自我更新；看下方日志，超过 30 分钟会超时 |
| DST 没识别到世界 | 世界目录里缺少 `server.ini`；面板只认带该文件的一级子目录 |
| DST 停止后还有进程 | 发送 `c_shutdown()` 后 10s 仍存活会杀进程树；若分片不读 stdin，直接走强杀 |
| 日志 `databundles/*.zip skipped` 且 `main.lua` 加载失败 | 工作目录必须是对应 `bin`/`bin64`（0.5.2 起已按 exe 所在目录启动） |
| 启动报“UDP 端口已被占用” | 旧服务器/其它程序占着端口；先在任务管理器结束旧的 DST 进程 |
| 日志出现 `CFileWriterThread` Assertion | 通常是进程提前退出后的次生错误；先看日志更上面的配置/路径错误，并确认端口未被占用 |
| 面板端口被其他程序占用 | 弹错误对话框；改 `conf/panel.json` 的 `listen` 或加 `-listen` 换端口 |
| 代理状态 `port not allowed` | 该 remotePort 不在 frps 的 `allowPorts` 内 |
| dashboard 401 | 用 TOML 里的 `webServer.user/password`；默认 admin |
| `config rejected by frpc` | 按提示修正；未知字段会被严格模式拒绝 |
| `frpc not found` | `bin/` 和 exe 同级都没有；删掉 `bin/` 让内置版本重新解压，或手工放入 |
| 托盘没有图标 | 远程会话/锁屏下可能注册失败；面板照常可用，退出用页面按钮或任务管理器 |
| 改了配置但没生效 | 运行中的 frpc 会自动 reload；frps 需要重启；dashboard 表单改的是 store.json |
| 日志乱码/带颜色码 | 面板返回日志时已去 ANSI；直接看 `logs/*.log` 可能仍带颜色，属正常 |

## 13. 安全

- 面板与 dashboard 都只绑定 `127.0.0.1`；不要做公网反向代理。
- dashboard 密码明文存在 `profiles/<id>/frpc.toml`，注意本机权限。
- `POST /api/quit` 无鉴权，但仅回环可达；它等价于“停止一切并退出”。
