# MSBOOST 一体化启动器开发规划

> 仓库：<https://github.com/mozziexwz/node_launcher>
> 日期：2026-10-12（2026-10-19 更新）　状态：M0 已完成（`docs/m0/README.md`）；M1 已完成并通过真实游戏验收（`docs/m1/README.md`）；M2 界面已通过实测验收并合并（`docs/m2/README.md`）；M3 打包已通过实测验收（`docs/m3/README.md`）；M4 改为虚拟机长时间测试，已通过；首个正式版本为 v1.0.0；M5 暂不做。v1.0.1 兼容其他代理软件的系统代理（§4.8），已通过实测验收（`docs/v1.0.1/README.md`）。v1.0.2 修复 Steam 版登录窗口黑屏，按确认的名单把加速进程调整为 30 项（§4.3，`docs/v1.0.2/README.md`），已通过实测验收。v1.0.3 修正加速页统计行一行两行来回跳（`docs/v1.0.3/README.md`）。
>
> 依据：
> - `legacy/MSBOOST启动器_全程TCP测速.bat`，以及 `docs/MSBOOST全程TCP测速整改计划.md`
> - 用户实测通过的 ProxyBridge 配置 `MapleStory.pbprofile`（进程清单与协议见 §4.3）
> - [ProxyBridge](https://github.com/InterceptSuite/ProxyBridge) v4.1.0 源码（commit `c9646aa`）
> - [mieru](https://github.com/enfein/mieru) v3.38.1 源码（2026-10-13 起；M0 前期用 v3.38.0）
> - 用户实测：ProxyBridge + MSBOOST.exe 代理冒险岛，登录、进频道、换频道均正常，反作弊无报错、无掉线，兼容性优于 Netch

## 1. 目标与范围

### 1.1 要解决的问题

现在用户要同时开两个程序：

- MSBOOST 启动器：BAT，负责运行 mieru 核心；
- Netch、Proxifier 或 ProxyBridge：负责把冒险岛流量送进本地 SOCKS5。

新程序把两者合并成一个带界面的 exe。用户的操作变成三步：导入节点配置 → 点“开始加速” → 打开冒险岛。

### 1.2 首版必须做到

1. 带界面的 Windows 程序，内置 mieru 客户端和按进程分流，不再依赖 MSBOOST.exe、Netch、Proxifier。
2. 只代理冒险岛相关进程的 TCP 和 UDP，其他程序一律直连。
   - 进程清单固定（§4.3），用户不能修改；
   - Discord、Steam 各有一个开关，可以一并加速，默认关闭。
3. 不改系统路由表、DNS、系统代理，也不装虚拟网卡。停止加速或程序异常退出后，网络立即恢复。
4. 覆盖旧 BAT 的全部功能：加载配置、启动/停止、出口 IPv4、到中转延迟、全程游戏延迟（v2 协议）。测速目标可以在设置中修改。
5. 开源（GPL-3.0），CI 可复现构建，以便携版发布。

### 1.3 不做

已确认不做（2026-10-13）：

- 用户自定义加速进程。只提供 Discord、Steam 两个开关。
- 代码签名。
- 面板账号登录、拉取配置。
- `mieru://` 链接导入。配置沿用现有流程：用户在网站下载配置文件，启动器读取。
- 安装包，首选便携版。

首版不做，列入 M5：服务模式（免每次 UAC）、自动更新。

另外：

- ARM64 Windows、Windows 7/8.1。
- 节点端任何改动。全程测速继续依赖主项目 v2.3.0 起随部署安装的 v2 测速服务。

### 1.4 设计原则

- **只碰冒险岛**：规则只匹配游戏进程。本程序自身流量（mieru 连节点、测速）始终直连，ProxyBridge 会按 PID 放行加载它的进程。
- **失败可回退**：启动时任一步失败，就逆序回滚，不留半开状态。
- **不留残留**：停止时关闭 WinDivert 句柄，并尝试停止驱动服务。设置页的“清理”功能会删除驱动服务、防火墙规则和释放出来的运行文件。
- **计时诚实**：沿用整改计划的测速口径，不叠加、不补零、不写死修正量。
- **界面只显示 MSBOOST**：
  - 窗口、托盘、对话框、提示和错误信息统一使用“MSBOOST”，例如“MSBOOST 节点配置”“MSBOOST 核心”；
  - 日志里组件统一称为“核心”“分流”，不出现 mieru、ProxyBridge 等组件名；
  - 组件名只出现在“关于”页的开源许可声明里。这是 GPL-3.0 的要求：交互界面要展示许可信息，并保留上游版权声明。

## 2. 规划依据（已核实的事实）

### 2.1 MSBOOST.exe 与 mieru

- MSBOOST.exe 就是 mieru 官方客户端 3.38.0（`MSBOOST.exe version` 的输出），许可为 GPL-3.0。
- 新程序嵌入的是 **3.38.1**。这一版修复了 SOCKS5 认证漏洞：
  - 3.38.0 的服务端在客户端同时提出“无认证”和“用户名密码”两种方式时，会放行“无认证”，本机任何程序都能绕过会话凭据（D3）。
  - 回归测试 `TestEngineNeverFallsBackToNoAuth` 已锁住这一点：它在 3.38.0 上失败，在 3.38.1 上通过。
- mieru 提供可嵌入的 Go 接口 `apis/client`：`NewClient`、`Store`、`Start`、`Stop`、`DialContext`。其中 `Store` 只接受单个 profile，实例 `Stop` 后不能复用。
- `mieru run` 的内部装配分两步：先用 `appctlcommon.NewClientMuxFromProfile` 建立多路复用，再用 `pkg/socks5` 的 `socks5.New` 在本地起 SOCKS5。这个 SOCKS5 支持入站用户名密码；`HANDSHAKE_NO_WAIT` 模式下会提前回复 SOCKS5 成功。
- 新程序在进程内复用这套装配，和 MSBOOST.exe 走的是同一条代码路径。
- `mieru run` 支持通过环境变量 `MIERU_CONFIG_JSON_FILE` 加载配置。这是嵌入方案失败时的退路（见 D2）。

### 2.2 ProxyBridge v4.1.0（MIT）

| 项 | 事实 | 对本项目的影响 |
|---|---|---|
| 结构 | C 核心 `ProxyBridgeCore.dll`（基于 WinDivert），导出 C API。官方界面通过 LocalSystem 服务 `ProxyBridgeSvc` 和命名管道驱动核心；以管理员运行时，也能直接加载 DLL | 我们直接加载 DLL，不用它的界面、命令行和服务 |
| 接口 | `AddProxyConfig`（支持 SOCKS5 用户名密码）、`AddRule`（进程名、目标、端口、协议、动作）、`Start`/`Stop`、日志回调与连接回调 | Go 用 `golang.org/x/sys/windows` 直接调用，不需要 cgo |
| 截获范围 | WinDivert 过滤器截获**全机**出站 TCP/UDP（含回环），不匹配规则的包原样回注；只有一个包处理线程 | 其他软件的包也要在用户态走一趟，必须做性能验收（§7.4、补丁 P2） |
| 回调线程 | 连接回调在包处理线程上**同步**调用，而且每条新连接都会触发，不只是命中规则的连接 | Go 回调必须非阻塞；默认不注册连接回调 |
| 自身流量 | 加载 DLL 的进程，其流量按 PID 一律直连 | mieru 跑在本进程内，不会形成回环 |
| 中继端口 | TCP 34010，UDP 34011 | 与已经在运行的 ProxyBridge 冲突，需要检测 |
| 防火墙 | `Start` 时用 netsh 为宿主 exe 添加入站放行规则，规则名为 `ProxyBridge relay <路径哈希>`；核心代码只添加，不删除 | 改名，并由“清理”功能删除（补丁 P1） |
| 驱动 | `Stop` 只关闭句柄，不卸载 WinDivert 驱动；官方卸载程序会执行 `sc stop WinDivert` | 我们在停止加速时主动停止驱动服务 |
| 进程名匹配 | 不区分大小写，支持通配符，例如 `MapleStory*.exe` | 私服改了名的客户端可以用通配符匹配 |

### 2.3 WinDivert 2.2.2-A

- 驱动已由作者做过 EV 签名，我们不重新编译。许可为 LGPL-3.0 / GPL-2.0 双许可。
- 官方发布包是 `WinDivert-2.2.2-A.zip`。ProxyBridge 上游工作流固定的 SHA-256 为 `63CB41763BB4B20F600B6DE04E991A9C2BE73279E317D4D82F237B150C5F3F15`，首次下载时需独立核对一次。
- 只支持 x86/x64，不支持 ARM64 Windows。

### 2.4 旧 BAT 的功能清单（新程序必须对齐）

- `[0]` 加载配置：列出同目录 `*.json`，调用 `replace config`；运行中禁止切换。
- `[1]`/`[2]` 启动、停止。
- `[3]` 网络检测，三项：
  - 出口 IPv4：经 SOCKS5 访问 `https://api-ipv4.ip.sb/ip`；
  - 到中转延迟：直连唯一的 TCP 入口，测 3 次，每次最多 2 秒；
  - 全程游戏延迟：经 SOCKS5 连到节点侧的 `127.0.0.1:20424`，走 v2 协议的 hello/connect。
- 状态存放在 `msboost-state.dat`，错误写入 `msboost-last-error.log`。
- 游戏测速目标写死为 `35.155.204.207:8585`。

## 3. 总体架构

### 3.1 组成

```text
MSBOOST-Launcher.exe（管理员运行，单进程）
├─ 界面：Wails v2 + WebView2，前端 React / TypeScript / Vite
├─ internal/app        编排：启动/停止状态机、向界面推送事件
├─ internal/engine     mieru：配置解析与校验、mux、本地 SOCKS5（127.0.0.1:随机端口 + 会话凭据）
├─ internal/redirector 分流：加载 ProxyBridgeCore.dll、下发规则、桥接回调、停止驱动服务
├─ internal/probe      网络检测：出口 IP、入口 TCP、全程 v2
└─ internal/winsys     系统：管理员、单实例、冲突检测、文件完整性、防火墙规则

同目录文件：ProxyBridgeCore.dll、WinDivert.dll、WinDivert64.sys
```

### 3.2 加速时的数据流

```text
MapleStory.exe ──TCP──▶ [WinDivert 驱动] ──改写──▶ ProxyBridge 中继（34010）
  ──SOCKS5（会话凭据）──▶ 本进程内的 mieru SOCKS5（127.0.0.1:随机端口）
  ──mieru──▶ MSBOOST 节点 ──▶ 冒险岛服务器

其他程序 ──▶ [WinDivert] ──原样回注──▶ 直连
本程序（mieru、测速）──▶ 按 PID 直连
```

### 3.3 关键决策

| 编号 | 决策 | 理由 | 备选或触发条件 |
|---|---|---|---|
| D1 | Go + Wails v2 + React/TS | mieru 是 Go 写的，可以进程内嵌入；前端与主项目同一技术栈 | Wails v3 目前还是 beta，转正后再评估（v3 自带托盘） |
| D2 | 进程内嵌入 mieru 3.38.1，不再调用 MSBOOST.exe | 一个进程一个生命周期；不在用户目录留下 mieru 配置库；没有后台残留进程和 RPC 端口；端口与凭据由程序自己掌控 | 若 M0 嵌入失败，改为托管子进程：设置 `MIERU_CONFIG_JSON_FILE` 后运行 `MSBOOST.exe run` |
| D3 | 本地 SOCKS5 只监听 127.0.0.1，用随机端口，每次启动生成随机用户名和密码 | 避免端口冲突；防止本机其他程序借用用户的节点 | — |
| D4 | ProxyBridge 用 v4.1.0 源码加少量补丁，在 CI 中编译成 DLL，由 Go 直接调用 | 复用你实测通过的核心；补丁小，便于审阅 | — |
| D5 | 首版整个程序以管理员运行（manifest 设为 `requireAdministrator`） | 最简单，与 Netch 一致 | M5 改为服务模式，免去每次 UAC |
| D6 | 默认不注册连接回调；日志回调只做非阻塞入队，队列满了直接丢弃 | 回调在包处理线程上同步执行，回调一慢，全机网络都会卡 | 诊断页打开时才临时注册连接回调 |
| D7 | 加速连接数等统计，从我们自己的 SOCKS5 入口计数 | 不占用包处理线程 | — |
| D8 | 许可证用 GPL-3.0 | mieru 是 GPL-3.0；ProxyBridge（MIT）和 WinDivert（LGPL-3.0）都与之兼容 | — |
| D9 | 程序文件名 `MSBOOST-Launcher.exe`，窗口标题“MSBOOST 启动器” | 不和旧的 MSBOOST.exe（mieru）重名 | 已确认 |
| D10 | 首版必须有托盘图标：Wails v2 + `fyne.io/systray`（Apache-2.0） | 用户要求；Wails v2 没有原生托盘 | M0 验证它与 Wails 消息循环能否共存；不行就自己用 `Shell_NotifyIcon` 实现，或改用 Wails v3 |
| D11 | 便携版：只有一个 `MSBOOST-Launcher.exe`；用户数据放在 exe 同目录 | 用户首选便携版；不需要安装和卸载 | 见 §6 M3 |
| D12 | 界面只显示 MSBOOST（§1.4） | 用户要求 | 组件名只出现在“关于”页 |

## 4. 功能规格

### 4.1 主页（加速）

- 一个主按钮：开始加速 / 停止加速。旁边显示当前节点配置名、加速对象（进程名）和运行时长。
- 三个状态灯：节点（MSBOOST 核心）、分流、测速服务（v2 hello，可选）。
- Discord、Steam 两个开关（默认关闭），见 §4.3。
- “网络检测”卡片显示出口 IPv4、到中转延迟、到游戏延迟。只在用户点“检测”时才运行，不做后台定时测速。
- 显示当前经过加速的冒险岛连接数，数据来自本地 SOCKS5 入口的计数。
- 开始加速时，如果冒险岛已经在运行，提示“已建立的连接不会被加速，请重启游戏”。
- 停止加速时，如果冒险岛在运行，要求二次确认，因为停止会断开游戏连接。

### 4.2 节点配置

- **来源**：沿用现有流程，用户在网站下载配置文件（例如 `直连.json`、`日本中转.json`），格式与旧 MSBOOST.exe `replace config` 相同。不做 `mieru://` 链接导入，也不对接面板账号。
- **读取方式**：
  - 程序读取 exe 同目录 `configs\` 文件夹里的 `*.json`，与旧 BAT 读取同目录配置的习惯一致；
  - 也可以在界面上点“导入配置”，选择文件后复制到 `configs\`；
  - 管理员程序收不到从资源管理器拖进来的文件（Windows 的 UIPI 限制），所以不提供拖放导入。
- **校验**：解析和校验复用核心自身的逻辑，失败时用 MSBOOST 的说法显示具体原因。
- **列表**：显示名称、服务器地址、TCP 端口，不显示密码。可以设为当前、删除。
- 运行中禁止切换配置，与 BAT 一致。
- 下列字段改由程序自己管理，读取时忽略：`socks5Port`、`socks5Authentication`、`httpProxyPort`、`rpcPort`。
- 配置文件里有节点密码。便携版的数据放在 exe 同目录，建议放在用户自己的文件夹，不要放进多人共用的目录。

### 4.3 游戏设置

- **加速进程**：最初原样沿用你实测通过的 ProxyBridge 配置（24 项）。v1.0.2 按你确认的名单调整为 30 项（详见 `docs/v1.0.2/README.md`）：
  - MapleStory：`MapleStory.exe`
  - Nexon 登录器与组件：`MapleBrowser_WZ2.exe`、`msw.exe`、`ms-launcher.exe`、`nexon_launcher.exe`、`nexon_client.exe`、`nexon_runtime.exe`、`nxsteam.exe`、`NexonPlug.exe`、`NGM.exe`、`NGM64.exe`、`NMService.exe`、`SIG.exe`、`DwarfAxe.exe`、`*\ToyWebView\cefhelper.exe`
  - 反作弊：`NGService.exe`、`NGClient64.aes`、`NGCLIENT.AES`、`grap-core64.aes`、`grap-communicator64.aes`、`grap-updater.aes`、`BlackCipher64.aes`、`BlackCall64.aes`、`BlackXchg.aes`
  - 崩溃上报：`UnityCrashHandler64.exe`、`CrashReportClient.exe`
  - 其他客户端：`MapleRoyals.exe`、`mr.dll`、`MapleLegends.exe`、`DreamMS.exe`

  与最初的配置相比：
  - 新增 `ms-launcher.exe`（Steam 版启动器，漏掉时登录窗口黑屏），以及几个网页组件和反作弊组件；
  - 移除 Kaizen、Fantasia；
  - `cefhelper.exe` 名字通用，按路径只匹配 MapleStory Worlds 的。

  清单固定在程序里（`internal/profiles`），**用户不能修改**；需要调整时随新版本发布。分流核心按分号分隔的清单匹配进程名，不区分大小写。
- **可选加速**：两个开关，默认关闭。
  - **Discord**：`Discord.exe`、`DiscordPTB.exe`、`DiscordCanary.exe`。语音走 UDP，也会一并加速。
  - **Steam**：`steam.exe`、`steamwebhelper.exe`。开关旁提示“Steam 下载也会经过节点，会消耗节点流量”。

  每个开关对应一条独立规则，可在加速中切换：只对之后新建的连接生效，已有连接要重连。M2 验证这一点。
- **协议**：TCP + UDP；目标主机、端口、域名都是“全部”；SOCKS5 发送域名（SendDomainToProxy）开启。均与实测配置一致。
  - 发送域名时由节点解析，节点端规则 `msboost-filter`（GFW 规则集）会拒绝其中的域名。M0 长时间测试中只出现过 1 次（`g.nexonstatic.com`），游戏不受影响。
  - v1.0.2 排查发现，它也会拒绝谷歌人机验证脚本所在的 `www.gstatic.com`，登录器因此偶尔转圈（`docs/v1.0.2/README.md` 第 4 节）。
- **测速目标**：在设置中可以自定义 IPv4 和端口，默认 `35.155.204.207:8585`。例如玩 MapleLegends 时可以改成它的服务器地址。校验规则与 v2 合同一致：必须是规范的公网 IPv4，端口在 1–65535。

### 4.4 网络检测（移植自 BAT 的 [3]）

- **出口 IPv4**：经本地 SOCKS5 访问 `https://api-ipv4.ip.sb/ip`。连接超时 10 秒，总超时 20 秒，只接受 IPv4。
- **到中转延迟**：本进程直接 TCP 连接配置中唯一的入口。“唯一入口”的判定沿用 BAT 的 `Get-LatencyTargets`：
  - 只有一个 server、一个 TCP 绑定，且没有 portRange；
  - 入口是域名时，必须只解析出一个 IPv4。

  测 3 次，每次最多 2 秒，取成功样本的均值。
- **到游戏延迟**：经本地 SOCKS5（带会话凭据）CONNECT 节点侧的 `127.0.0.1:20424`，执行 v2 hello/connect。暖机共享 5 秒，每次请求 2 秒，最多 3 次。计时口径与整改计划完全一致。
- **与旧合同的唯一差异**：客户端 SOCKS5 支持用户名密码认证，只用于本程序自己的会话凭据。其余字段、校验、超时和重连规则都不变。
- 三项互相独立，任一项失败不影响另外两项。失败原因沿用 BAT 的中文提示。
- WinDivert 运行时，本程序的测速包也会在用户态回注一次，预计对入口延迟的影响很小，M0 实测确认。

### 4.5 日志与诊断

- 日志放在 exe 同目录的 `logs\`，按天滚动，保留 7 天。
  - 核心、分流和程序本身的日志分别打上 `[核心]`、`[分流]`、`[程序]` 标签。
  - 写盘前去掉节点密码和会话凭据。
- 诊断页显示：
  - 最近的错误；
  - 版本：启动器版本，以及核心、分流组件的版本号。组件名只在“关于”页出现；
  - “连接监视”开关：打开时临时注册连接回调，只显示冒险岛进程的连接。
- “关于”页：许可信息（GPL-3.0、无担保声明），以及开源组件与许可（mieru、ProxyBridge、WinDivert、Wails 等）。
  - “连接监视”开关：打开时临时注册连接回调，只显示冒险岛进程的连接。
- “导出诊断包”：把日志和去敏后的设置打成 zip，方便用户反馈问题。

### 4.6 启动与停止流程

**开始加速**（任一步失败，都逆序回滚并显示原因）：

1. 自检：
   - 管理员权限、单实例；
   - 三个二进制文件的 SHA-256 与构建时记录的一致；
   - 冲突检测（§4.7）。
2. 读取当前节点配置，取 active profile，建立 mieru mux。
3. 在 `127.0.0.1:0` 上起 SOCKS5，生成随机用户名和密码。
4. 配置 ProxyBridge 并启动：
   1. 设置日志回调（非阻塞）；
   2. 关闭流量日志；
   3. `SetLocalhostViaProxy(FALSE)`；
   4. `AddProxyConfig(SOCKS5, 127.0.0.1, 端口, 用户名, 密码, 发送域名=TRUE)`；
   5. 调用一次 `AddRule(冒险岛进程清单（分号分隔）, "*", "*", "", BOTH, PROXY, 配置)`；每个打开的可选加速（Discord、Steam）再各加一条规则；
   6. `Start()`。
5. 进入“加速中”。后台查询一次出口 IP，作为连通性提示；查询失败只警告，不自动停止。

**停止加速**：

1. 在后台调用 `ProxyBridge_Stop()`，界面显示“正在停止”。上游的停止流程最长可能等待 35 秒以上。
2. 删除本次添加的规则和代理配置。
3. 尝试停止 `WinDivert` 驱动服务。如果被其他软件占用，会停止失败，这时只记录、不报错。
4. 关闭 SOCKS5，停止 mieru。

**退出程序**：如果正在加速，先完成停止流程。不调用 `FreeLibrary`，避免上游线程还没退出时 DLL 已被卸载。

**异常退出**：WinDivert 句柄随进程一起关闭，其他软件的网络立即恢复。下次启动时清理遗留状态。

### 4.7 冲突检测（开始加速前）

- `ProxyBridgeSvc` 服务或 ProxyBridge 程序正在运行：中继端口和截获规则会冲突，提示先关闭。v1.0.1 起提示条上有“停止 ProxyBridge”按钮，确认后由程序让 ProxyBridge 正常退出（向它的窗口发送托盘菜单的“退出”命令），并停止它的服务，用户不用再输入命令。
- Netch 正在运行：提示可能重复代理。
- 端口 34010 或 34011 被其他程序占用：提示是哪个进程。
- 其他 WinDivert 程序（如 GoodbyeDPI）：只提示，一般可以共存。

### 4.8 与其他代理软件共存（v1.0.1）

问题：v2rayN、Clash 系列等软件开启“系统代理”后，Windows 的代理设置指向本机端口（如 `127.0.0.1:10808`）。MapleStory 登录器遵守这个设置，把请求发给那个端口。分流核心对发往本机的连接一律直连，于是登录器的地区检查走了那些软件的节点；节点不在美国时，就会提示“所在地区不支持”。

做法（`internal/sysproxy`、补丁 P4）：
1. 开始加速时通过 WinHTTP 的 `WinHttpGetIEProxyConfigForCurrentUser` 读取当前用户的系统代理设置（手动代理地址；PAC 模式下读取脚本并找出其中的代理地址），和浏览器读取的是同一份设置。任何“系统代理”类软件改的都是这份设置，所以不需要逐个适配。不直接读注册表里的代理键：那正是劫持浏览器代理的恶意软件的典型特征，会让 Chrome 把程序判为危险下载（见 `docs/v1.0.1/README.md`）。
2. 在游戏规则之前加一条规则：游戏和已开启的可选加速项的进程，发往系统代理地址和端口的 TCP 连接，交给程序内的“代理请求处理器”。规则按顺序匹配，所以这条必须在前；之后只原地修改，不重新添加。
3. 系统代理在本机时，用补丁 P4 告诉分流核心这些本机端口可以按规则代理（其他本机端口和 UDP 仍然直连）。
4. 代理请求处理器代替那个代理软件回应游戏：支持 HTTP（CONNECT 和普通请求）、SOCKS5 和 SOCKS4/4a，再通过 MSBOOST 核心把真实请求发往节点（域名由节点解析）。
5. 加速中每 5 秒重新读取一次系统代理，开关代理软件后自动更新。

其他软件照常使用它们的代理，不受影响。TUN（虚拟网卡）模式不设系统代理，不会出现上述问题；但 MSBOOST 自己连节点的流量可能被它再代理一层，暂不处理。

### 4.9 旧版迁移

- 提供“从旧版导入”：选择旧 BAT 所在的文件夹，读取 `msboost-state.dat` 里的 `ConfigFile`，导入同目录的 JSON 配置。
- 旧 BAT 和 MSBOOST.exe 不必删除，新程序不依赖它们。

## 5. 仓库结构

```text
node_launcher/
├─ main.go / app.go / wails.json   Wails 入口
├─ internal/
│  ├─ app/          状态机，暴露给前端的方法与事件
│  ├─ engine/       mieru 嵌入：配置、mux、本地 SOCKS5
│  ├─ redirector/   ProxyBridgeCore.dll 绑定、规则、回调、驱动服务控制
│  ├─ probe/        出口 IP、入口 TCP、全程 v2（含 SOCKS5 客户端）
│  ├─ profiles/     游戏方案、进程名校验
│  ├─ store/        settings.json、节点配置副本、路径
│  ├─ winsys/       管理员、单实例、冲突检测、文件完整性、防火墙规则
│  └─ logx/         日志与去敏
├─ frontend/        React + TypeScript + Vite
├─ third_party/
│  ├─ ProxyBridge/  git submodule，固定在 v4.1.0（c9646aa）
│  ├─ patches/      对 ProxyBridge 的补丁，附说明 PATCHES.md
│  └─ windivert.sha256
├─ scripts/         build-core.ps1、fetch-windivert.ps1、package.ps1
├─ build/windows/   图标、manifest（requireAdministrator）、版本信息
├─ testdata/        节点端 tcp_probe.py 的固定副本（注明来源提交）、示例配置（假凭据）
├─ cmd/m0spike/     M0 验证工具（命令行），M1 后由正式入口取代
├─ legacy/          旧 BAT 和 PowerShell 测试，仅供移植参考，不参与构建
├─ docs/            本规划、架构说明、发布流程
├─ _local/          本地资料（旧版原件、真实节点配置），已加入 .gitignore
├─ LICENSE          GPL-3.0
└─ THIRD_PARTY_NOTICES.md
```

`.gitignore` 必须排除：

- 构建产物：`*.exe`、`*.dll`、`*.sys`；
- `msboost-state.dat`；
- 任何真实的节点配置 JSON，因为里面有节点密码。

### 5.1 本目录现有文件的去向

原件全部移到本地的 `_local/legacy-original/`（已加入 `.gitignore`，不提交），仓库里只放副本。仓库是公开的，所以副本中的两个真实节点 IP 已换成文档保留地址 `192.0.2.10`、`192.0.2.20`。

| 文件 | 仓库中的位置 |
|---|---|
| `MSBOOST启动器_全程TCP测速.bat`、`tests/*.ps1` | `legacy/`，作为移植和测试用例的来源（BAT 与原件逐字节一致） |
| `MSBOOST全程TCP测速整改计划.md` | `docs/`，作为 v2 测速合同的出处 |
| `server/`（v1、v2） | 不进仓库；节点端以主项目的 `installers/node/tcp_probe.py` 为准 |
| `MSBOOST.exe`、`msboost-state.dat` | 不进仓库 |

## 6. 里程碑

### M0 技术验证

目标：在大量写代码之前，先把所有没验证过的假设验证掉。

| # | 验证项 | 通过标准 |
|---|---|---|
| 1 | 用 MSVC 编译 ProxyBridgeCore.dll（v4.1.0）；下载并核对 WinDivert 2.2.2-A | DLL 能加载，哈希一致 |
| 2 | Go（不用 cgo）调用 DLL：AddProxyConfig、AddRule、Start、Stop，并注册日志回调 | 由原生线程触发的回调，在 1 小时压力下无崩溃、无死锁 |
| 3 | 进程内嵌入 mieru 3.38.0，用 `直连.json` 起本地 SOCKS5（随机端口 + 凭据） | 出口 IP 与 MSBOOST.exe 一致；全程延迟与 BAT 的实测在同一量级 |
| 4 | 把 2 和 3 组合起来，实测冒险岛 | 登录、进频道、换频道、商城正常；反作弊无报错；30 分钟不掉线；延迟与“ProxyBridge + MSBOOST.exe”相差不超过 5 ms |
| 5 | 停止后的驱动状态 | `sc query WinDivert` 显示已停止或不存在 |
| 6 | 防火墙规则是否必要 | 在“公用网络”下禁用该规则再测；按结果决定补丁 P1 的做法 |
| 7 | 性能基线 | 记录加速开、关两种情况下，其他程序的下载速度和连接耗时 |
| 8 | Wails v2 以管理员运行 | 窗口、WebView2、文件选择框都正常 |
| 9 | 托盘图标（D10） | 托盘与 Wails 窗口共存；菜单能开始/停止加速、显示窗口、退出；退出后图标消失 |
| 10 | UDP 经嵌入的 mieru 转发（SOCKS5 UDP ASSOCIATE + 会话凭据） | 规则为 BOTH 时，冒险岛相关进程的 UDP 走代理且游戏正常 |

退出标准：第 1–5 项和第 9、10 项通过，第 6、7 项有记录。

补充说明（2026-10-12）：

- 本机暂时没有 C 编译器。第 2–7、10 项的行为测试先用本机已安装的官方 ProxyBridge 4.1.0 的 `ProxyBridgeCore.dll`，它与子模块固定的源码版本相同。
- 第 1 项（自行编译）等本机装好 MSVC 后完成，或由 CI 完成。
- 测试前必须先停止官方 ProxyBridge 的 `ProxyBridgeSvc` 服务，否则中继端口和截获规则会冲突。任何一项不通过，先按 D2 的备选或 §9 调整方案，再进入 M1。

### M1 核心引擎（不含界面）

- 实现 `engine`、`redirector`、`probe`、`profiles`、`store`、`winsys`、`logx` 几个包。
- 提供一个只供开发用的无界面入口（如 `--headless`），不开界面也能完成导入、加速、检测、停止。
- 移植测速测试（§7.1），实现状态机与回滚。
- 退出标准：`go test ./...` 全部通过；用无界面入口实测冒险岛，结果同 M0 第 4 项。

### M2 界面与功能对齐

- 五个页面：主页、节点配置、游戏设置、日志/诊断、设置/关于。
- 单实例（Wails `SingleInstanceLock`）、关闭窗口确认、冲突检测、旧版迁移、导出诊断包。
- 托盘：Wails v2 没有原生托盘，需要评估第三方库；不稳定的话首版不做，只最小化到任务栏。
- 退出标准：BAT 的每个功能在界面上都有对应项；中文文案审阅完成。

### M3 打包与发布（便携版）

- GitHub Actions（Windows 托管 runner）依次执行：
  1. 下载并校验 WinDivert；
  2. 用 MSVC 编译 ProxyBridgeCore.dll；
  3. `go vet`、`go test`；
  4. 前端构建与测试；
  5. `wails build`；
  6. 打成 zip。
- **产物**：
  - 一个 `MSBOOST-Launcher.exe`，内嵌 `ProxyBridgeCore.dll`、`WinDivert.dll`、`WinDivert64.sys`；
  - 外加 `SHA256SUMS`，一起打成 zip。不做安装包。
  - zip 里还要有 `LICENSE.txt`（GPL-3.0 全文）和 `THIRD_PARTY_NOTICES.txt`（各组件和 Go 依赖的版权声明与许可全文，由 `scripts/notices` 生成）。“开源许可”对话框保留组件表，并注明完整声明见该文件（对话框里组件名照用户要求显示，例如 mieru 显示为“見える”）。
  - 程序里不显示源代码地址，所以网站下载页要在下载链接旁注明源代码地址（GPL-3.0 第 6 条 d 款）。
- **运行文件释放**：
  - 程序以管理员身份启动时，把内嵌的三个文件释放到 `%ProgramData%\MSBOOST\runtime\<版本哈希>\`。这个目录只允许管理员和 SYSTEM 写入。
  - 每次启动都校验 SHA-256，不一致就重新释放，然后从这里加载 DLL。
  - 原因：便携 exe 常放在“下载”这类普通用户可写的目录。如果 DLL 和 exe 放在一起，可能被替换后以管理员身份加载。
- **用户数据**：放在 exe 同目录，包括 `configs\`（节点配置）、`logs\`、`settings.json`、`data\`（界面缓存）。
- **清理**：便携版没有卸载程序，设置页提供“清理”功能：
  1. 停止并删除 WinDivert 驱动服务（没有被其他软件占用时）；
  2. 删除本程序的防火墙规则；
  3. 删除 `%ProgramData%\MSBOOST\`。

  之后删掉 exe 所在的文件夹即可。
- **不做代码签名（已确认）**：发布说明中写明两点：
  - SmartScreen 拦截时点“更多信息 → 仍要运行”；
  - 杀毒软件误报时怎么处理：加白名单，或向厂商提交误报。
- 补齐 THIRD_PARTY_NOTICES、关于页的许可信息、发布说明模板。
- 退出标准：在干净的虚拟机上解压即用；执行“清理”后没有残留（§7.4）。

### M4 验收与小范围试用

- 完成 §7.3 真实链路验收、§7.4 不影响其他软件的验收、§7.5 兼容性矩阵。
- 找 5–10 名用户试用一周，收集诊断包；修复问题后发布 v1.0.0。

**实际执行（2026-10-14）**：找不到足够的试用者，改为在全新安装的 Windows 10 和 Windows 11 虚拟机上，加上本机，各长时间加速约 10 小时。三份诊断包都没有错误，网络检测稳定（到中转约 55 ms，到游戏约 153 ms）。据此直接发布正式版 v1.0.0；之后的问题由用户导出诊断包反馈。

### M5 后续（不在首版）

- 服务模式：由常驻服务持有驱动，界面不再需要管理员权限（参考 ProxyBridge 的 `ProxyBridgeSvc` 设计）。
- 自动更新（待定）。
- 低影响模式：按游戏端口收窄 WinDivert 过滤器，是补丁 P2 的进一步版本，视性能数据决定。

**决定（2026-10-14）：暂不做。**
- 服务模式需要安装系统服务，和便携版的定位冲突，还会多出一个常驻的高权限组件。每次打开时弹管理员提示，和 Netch 一样，可以接受。
- 自动更新：如果以后发版频繁，可以先做一个轻量的“发现新版本时提示并打开下载页”，不自动替换文件。
- 低影响模式：三台机器各 10 小时的测试都没有发现其他软件变慢，等有用户反馈再做。

已确认不做：面板对接、`mieru://` 链接导入、代码签名、用户自定义进程（§1.3）。

## 7. 测试与验收

### 7.1 自动化测试（在 CI 中运行）

- **测速协议**：
  - 把 `tests/Test-FullPathBoundaries.ps1` 的 64 个边界用例、`tests/Test-FullPathLatency.ps1` 的 20 组用例逐条改写成 Go 表驱动测试，使用进程内的假 SOCKS5 和假 v2 节点。
  - 新增 SOCKS5 用户名密码认证的用例：凭据正确、凭据错误、服务端不支持认证。
- **跨实现契约测试**：在 CI 中用 Python 运行主项目 `tcp_probe.py` 的固定副本，Go 客户端经假 SOCKS5 执行 hello/connect，确认两端实现一致。
- **配置解析**：覆盖以下情况：
  - 有效配置、多个 profile、缺少 activeProfile；
  - 没有 TCP 绑定、只有 UDP 绑定、带 portRange；
  - 域名入口解析出多个 IPv4；
  - 日志中的密码已去敏。
- **状态机**：在每一步注入失败，断言能逆序回滚，并且没有残留监听。
- **前端**：关键页面的组件测试。

### 7.2 Windows 集成测试

需要管理员权限，先在本地虚拟机上跑；如果 GitHub 托管 runner 能加载驱动，再放进 CI。

1. 准备两个测试程序，一个命名为 `MapleStory.exe`，一个命名为 `other.exe`，都去连接 `203.0.113.10:8585`。这是文档保留地址，在真实网络中不可达。
2. 在本地起一个会计数的假 SOCKS5 作为上游，然后开始加速：
   - `MapleStory.exe` 的连接应该出现在假 SOCKS5 中，并且连接成功；
   - `other.exe` 应该直连超时，假 SOCKS5 中没有它的记录。
3. 停止加速后，两个程序都不再经过假 SOCKS5，`sc query WinDivert` 显示已停止。
4. 强制结束本程序后，`other.exe` 立即恢复正常的直连行为。

### 7.3 真实链路验收（不能用模拟代替）

- [ ] 真实节点加真实冒险岛：登录、进频道、换频道、商城、组队都正常；1 小时不掉线；反作弊无提示。
- [ ] 延迟对比：本程序与“ProxyBridge + MSBOOST.exe”相差不超过 5 ms。
- [ ] 同一节点、同一时段，网络检测三项与旧 BAT 的结果一致。
- [ ] 切换节点配置后重新加速，出口 IP 和延迟随之变化。
- [ ] 旧节点（没有 v2 测速服务）：游戏加速正常；到游戏延迟显示“不兼容/不可达”；另外两项正常。
- [ ] 切换 Wi-Fi、睡眠唤醒之后，加速能自动恢复，或者给出明确提示。

### 7.4 不影响其他软件的验收

- [ ] 加速中：浏览器、Steam/Epic 下载、Discord 语音、其他游戏都正常；诊断页中它们的连接全部为 DIRECT。
- [ ] 性能：对比加速开、关两种情况下其他程序的下载速度和 TCP 连接耗时，结果写进发布说明。
  - 建议目标：下载速度下降不超过 5%，连接耗时增加不超过 1 ms。
  - 达不到目标时，启用补丁 P2 后复测。
- [ ] 停止加速后与安装前对比：`route print`、DNS 设置、系统代理、`netsh winhttp show proxy` 都没有变化；WinDivert 已停止；没有残留进程。
- [ ] 执行“清理”并删除程序文件夹后：没有 WinDivert 驱动服务（未被其他软件使用时）、没有本程序的防火墙规则、没有 `%ProgramData%\MSBOOST\`。

### 7.5 兼容性矩阵

| 系统 | 内存完整性 | 杀毒软件 |
|---|---|---|
| Windows 10 22H2 x64 | 关 | Defender / 火绒 / 360 |
| Windows 11 24H2 x64 | 开、关 | Defender / 火绒 / 360 |
| Windows 11 最新版 x64 | 开 | Defender |

另外测试与以下软件同时运行时的表现，写进常见问题：Clash（系统代理模式或 TUN 模式）、GoodbyeDPI、Netch。

## 8. 构建、发布与许可

- **固定版本**：
  - Go 1.27.x（与主项目一致）、Node 24；
  - Wails v2.16.0（M0 锁定）、`fyne.io/systray` v1.12.2；
  - mieru v3.38.1、ProxyBridge v4.1.0（`c9646aa`）；
  - WinDivert 2.2.2-A，SHA-256 见 §2.3。
- **升级 mieru**：先跑契约测试，再用真实节点验证与节点端 mihomo 的兼容性。
- **升级 ProxyBridge**：重新应用补丁，再跑 §7.2 的集成测试。
- **安全**：
  - DLL 只从受保护的释放目录 `%ProgramData%\MSBOOST\runtime\` 按绝对路径加载（用 `LoadLibraryEx` 并限定搜索目录），加载前校验 SHA-256，防止 DLL 劫持（M3）。
  - 便携 exe 本身可以放在任何位置。
- **许可**：
  - 本仓库使用 GPL-3.0。
  - THIRD_PARTY_NOTICES 列出：mieru（GPL-3.0）、ProxyBridge（MIT，保留其版权声明）、WinDivert（LGPL-3.0）、Wails（MIT），以及前端依赖。
  - 产品名不使用 ProxyBridge、InterceptSuite 字样，在关于页致谢。

### 8.1 对 ProxyBridge 的补丁（初版，尽量少）

| 编号 | 内容 | 原因 | 何时做 |
|---|---|---|---|
| P1 | 防火墙规则改名为 `MSBOOST relay <哈希>`；如果 M0 证明不需要，就直接去掉 | 便于识别和清理 | M1 |
| P2 | 没有 UDP 规则时，过滤器不截获 UDP（包括 DNS 嗅探） | 减少对其他软件（语音、其他游戏）的影响 | 由 M0 的性能数据决定 |
| P3 | 导出中继端口的占用状态，或允许自定义端口 | 端口冲突时能给出明确提示 | 按需 |
| P4 | 新增 `ProxyBridge_SetLocalhostProxyPorts`：列出的本机 TCP 端口可以按规则代理 | 接管游戏发往系统代理的请求（§4.8） | v1.0.1 |

每个补丁都附说明和对应测试，记录在 `third_party/patches/PATCHES.md`。

## 9. 风险与应对

| 风险 | 影响 | 应对 |
|---|---|---|
| 杀毒软件误报 WinDivert 或未签名的 exe | 程序无法启动，或文件被隔离 | 不做代码签名（已确认）；向火绒、360、微软提交误报申诉；发布说明和启动自检给出加白名单的指引 |
| 冒险岛反作弊以后拦截 WinDivert | 无法加速 | 关注试用反馈；备选方案为 NetFilter SDK 商业授权，或 mihomo TUN 路由模式 |
| WinDivert 驱动停止失败（被其他软件占用） | 其他游戏的反作弊可能检测到它 | 停止加速时尝试卸载；在常见问题中说明“重启电脑即可清除” |
| 截获全机流量带来性能损耗 | 影响其他软件的网速 | 按 §7.4 测量；补丁 P2；后续做低影响模式 |
| 回调阻塞包处理线程 | 全机网络卡顿 | 默认不注册连接回调；日志回调非阻塞，满了直接丢弃 |
| ProxyBridge 上游更新很快（v4.0.0 与 v4.1.0 相隔很近） | 合并上游变更困难 | 固定版本；补丁尽量少；升级时必须跑集成测试 |
| mieru `pkg/` 内部接口变化 | 升级时编译失败 | 固定 3.38.1；把调用都封装在 `engine` 包内；退路是 `apis/client` 加自己实现的 SOCKS5 |
| 管理员程序被 DLL 劫持 | 被提权 | 按绝对路径加载；推荐安装版；文件完整性自检 |
| 已经在运行的冒险岛没有被加速 | 用户以为加速无效 | 开始加速时检测并提示重启游戏 |
| 节点配置里的密码明文保存 | 本机泄露风险 | 存在用户目录；日志去敏；M5 可选 DPAPI 加密 |

## 10. 确认事项

已确认（2026-10-12）：

1. 进程清单与协议：以 `MapleStory.pbprofile` 为准，共 24 项，TCP + UDP（§4.3）。v1.0.2 调整为 30 项，见 §4.3。
2. 程序文件名 `MSBOOST-Launcher.exe`，窗口标题“MSBOOST 启动器”（D9）。
3. 首版需要托盘图标（D10，M0 第 9 项）。

已确认（2026-10-13）：

4. 加速进程清单固定，用户不能自定义；另加 Discord、Steam 两个可选开关，默认关闭（§4.3）。
5. 测速目标不写死，可以在设置中修改（§4.3）。
6. 界面统一显示 MSBOOST，不出现 mieru（§1.4、D12）。
7. 首选便携版；不做代码签名、面板对接和 `mieru://` 链接导入；沿用“网站下载配置文件、启动器读取”的方式（§1.3、§4.2、M3）。
8. 核心使用 mieru 3.38.1（§2.1）。

目前没有待确认事项。
