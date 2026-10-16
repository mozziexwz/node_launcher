# v1.0.2：修复 Steam 版登录窗口黑屏，整理加速名单

## 1. 问题

1. 玩家反馈：开着 MSBOOST 加速，通过 Steam 启动 MapleStory 时，弹出的“NEXON”登录窗口一直是黑屏。
2. 玩家反馈：Nexon 登录器偶尔在点“LOG IN”后一直转圈。关掉登录器、重新开始加速后再打开，就好了。

## 2. Steam 版登录窗口黑屏

玩家提供了 Steam 版安装目录（`steamapps\common\MapleStory`）的截图。它和 Nexon 登录器版不同：

| 位置 | 内容 |
|---|---|
| `mslauncher\ms-launcher.exe` | Steam 版的启动器，基于 Electron（内嵌 Chromium）；Nexon 登录器版没有 |
| `MapleStory.exe`、`MapleBrowser_WZ2.exe` | 游戏本体和游戏内浏览器 |
| `NxOverlay\DwarfAxe.exe` | Nexon 游戏内覆盖层的网页组件，基于 CEF |
| `BlackCipher` 目录 | 主游戏的反作弊 |
| `MSCW` 目录 | MapleStory Classic World：自己的 `MapleStory.exe`、`NxOverlay`，以及反作弊目录 `grap`（NGS-X） |

- 从 Steam 启动时不经过 Nexon 登录器，而是先运行 `ms-launcher.exe`。截图里 800×600 的“NEXON”窗口就是它。
- Electron 程序负责显示网页和联网的子进程都和主程序同名，也叫 `ms-launcher.exe`。
- `ms-launcher.exe` 不在加速名单中，所以登录页面直连 Nexon，没有经过 MSBOOST 节点。
  - 直连会受网络环境和地区检查影响，和 v1.0.1 修复的“所在地区不支持”是同一类问题。
  - 页面加载不出来，窗口就是黑屏。
- 通过 Nexon 登录器启动时，登录页面由 `nexon_launcher.exe`、`nexon_client.exe` 加载，它们在名单里，所以一直正常。

## 3. 加速名单（共 30 项）

按 2026-10-17 确认的名单，原来的 24 项调整为 30 项：

| 分组 | 进程 |
|---|---|
| MapleStory | `MapleStory.exe` |
| Nexon 登录器与组件 | `MapleBrowser_WZ2.exe`、`msw.exe`、`ms-launcher.exe`、`nexon_launcher.exe`、`nexon_client.exe`、`nexon_runtime.exe`、`nxsteam.exe`、`NexonPlug.exe`、`NGM.exe`、`NGM64.exe`、`NMService.exe`、`SIG.exe`、`DwarfAxe.exe`、`*\ToyWebView\cefhelper.exe` |
| 反作弊 | `NGService.exe`、`NGClient64.aes`、`NGCLIENT.AES`、`grap-core64.aes`、`grap-communicator64.aes`、`grap-updater.aes`、`BlackCipher64.aes`、`BlackCall64.aes`、`BlackXchg.aes` |
| 崩溃上报 | `UnityCrashHandler64.exe`、`CrashReportClient.exe` |
| 其他客户端 | `MapleRoyals.exe`、`mr.dll`、`MapleLegends.exe`、`DreamMS.exe` |

与 v1.0.1 相比：
- 新增 9 项：
  - `ms-launcher.exe`：Steam 版启动器（第 2 节）；
  - `MapleBrowser_WZ2.exe`、`DwarfAxe.exe`、`cefhelper.exe`：游戏内浏览器、游戏内覆盖层、MapleStory Worlds 的内嵌网页。网页由这些组件加载和联网，而不是游戏本体；
  - `BlackXchg.aes`：主游戏反作弊 BlackCipher 的独立进程，自己会联网；
  - `grap-core64.aes`、`grap-communicator64.aes`、`grap-updater.aes`、`NGService.exe`：反作弊 NGS-X，MapleStory Classic World 和 MapleStory Worlds 都用它。
- 移除 3 项：`Kaizen v92.exe`、`Kaizenv92.exe`、`Fantasia.exe`。
- `msw.exe`、`MapleBrowser_WZ2.exe` 移到“Nexon 登录器与组件”组。

依据：扫描了本机所有 Nexon 目录，并结合 Steam 版截图，逐个查看文件说明、所在目录，以及程序是否引用联网相关的库。几点说明：
- `cefhelper.exe` 这个名字别的软件也用，所以写成 `*\ToyWebView\cefhelper.exe`。
  - 分流核心遇到含路径的写法，会和程序的完整路径比较，只命中 MapleStory Worlds 自己的。
  - 它也不参与“游戏正在运行”的检查。
- `BlackCall64.aes`、`NGClient64.aes`、`grap-communicator64.aes`、`mr.dll` 是 DLL，加载在别的进程里运行，不会单独出现。留在名单里没有影响。
- 检查过、不需要加入的：
  - `nexon_updater.exe`：登录器自我更新用的副本；
  - `nexon_agent.exe`：登录器的后台代理；
  - `NexonLauncher64.exe`：韩国 Nexon 平台的后台服务，主要做本机通信和日志上报；
  - MapleStory Worlds 的创作者工具，以及运行库安装程序。

## 4. 登录器偶尔转圈

### 诊断

截图中登录页面写着“protected by reCAPTCHA Enterprise”，点 LOG IN 后按钮一直转圈。
- 诊断包的系统代理接管记录显示，登录页面会访问 `recaptcha.net` 和 `www.gstatic.com`。
- 这两个是谷歌人机验证用的，验证脚本放在 `www.gstatic.com` 上。

节点（MSBOOST.DE 的节点安装脚本）的规则顺序：
1. 放行清单 `msboost-direct`：Nexon、MapleStory、私服、Steam 等域名；
2. GFW 域名列表 `msboost-filter`：拒绝；
3. 其余直连。

用同一个节点实测，完成 TLS 握手才算连通：

| 目标 | 把域名交给节点 | 把 IP 交给节点 |
|---|---|---|
| `www.gstatic.com`（人机验证脚本） | ❌ 被拒绝 | ✅ |
| `www.google.com` | ❌ 被拒绝 | ✅ |
| `connect.facebook.net` | ❌ 被拒绝 | ✅ |
| `recaptcha.net`、`www.recaptcha.net` | ✅ | — |
| Nexon 各域名、`g.nexonstatic.com`、`region1.google-analytics.com` | ✅ | — |

### 原因

- MSBOOST 开启了“发送域名”，让节点解析域名，避免本地 DNS 污染。
  - 分流核心只有截获到程序自己的 DNS 查询，才知道某个 IP 对应的域名，这时交给节点的是域名；
  - 否则交的是 IP。
- 交的是域名时，`www.gstatic.com` 被节点拒绝，人机验证脚本加载失败。点 LOG IN 后拿不到验证结果，按钮就一直转圈。
- 交的是 IP 时，节点不按域名过滤，一切正常。
- 开着 v2rayN 等系统代理时，登录器的请求由 MSBOOST 接管。接管时交的总是域名，更容易遇到。
- 自动登录，或者验证脚本已经在缓存里时，不需要再下载脚本。所以只是偶尔出现；重开登录器、重新加速后情况变了，就“好了”。

### 修复

在节点端修复，启动器不用改。完整的复查结果和要加的规则见本目录的 `节点放行清单整改计划.md`：
- 必须：在放行清单中加入 `DOMAIN,www.gstatic.com`，只放行这一个主机；
- 建议：再加入 `DOMAIN-SUFFIX,recaptcha.net` 和 `DOMAIN-SUFFIX,nexonstatic.com`，以防它们以后被列入 GFW 列表。

Google、Facebook、X 等第三方账号登录要访问的域名仍会被节点拒绝；Apple 账号登录不受影响。加速时请用 Nexon 账号密码或 Apple 账号登录。

### 另一个现象：节点连接短暂中断

16 日 02:53 到 02:56 约 2.5 分钟里，所有经过节点的连接都失败了，UDP 中继也断开，之后自动恢复。
- 那段时间如果打开登录器，同样会转圈；重新开始加速可以马上恢复。
- 之后连续 8 小时和第二天 7 小时都没有再出现。

## 5. 实测步骤

### 测试 1：通过 Steam 启动

需要一台装有 Steam 版 MapleStory 的电脑。

1. 下载测试包，解压，导入节点配置，开始加速。
2. 在 Steam 中启动 MapleStory。
3. “NEXON”登录窗口应正常显示登录界面，登录后能进入游戏。

### 测试 2：通过 Nexon 登录器启动

确认和 v1.0.1 一样正常。

### 测试 3：界面

“游戏与加速项”页面的分组和第 3 节的表格一致。

### 测试 4（节点放行 `www.gstatic.com` 之后）：手动登录

在登录器中退出账号，再用账号和密码手动登录，应能完成人机验证并登录。可以开着 v2rayN 的系统代理再试一次。

### 如果还有问题

请记下卡在哪一步并截图，同时附上日志（`logs\launcher-日期.log`）。日志不要公开发布。

## 6. 实测结果

| 测试包 | 日期 | 结果 |
|---|---|---|
| dev-022fcb9 | 2026-10-16 | 本机加速三次都正常，其中两次分别连续运行约 8 小时和 7 小时 |
| dev-620199e（30 项名单） | 2026-10-17 | 实测通过 |

测试 4（节点放行 `www.gstatic.com` 后的手动登录）等节点更新后再做。
