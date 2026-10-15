# v1.0.2：修复 Steam 版登录窗口黑屏

## 1. 问题

玩家反馈：开着 MSBOOST 加速，通过 Steam 启动 MapleStory 时，弹出的“NEXON”登录窗口一直是黑屏，登录界面加载不出来。

## 2. 原因

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

检查目录时还发现几处同类遗漏：
- `DwarfAxe.exe`、`MapleBrowser_WZ2.exe`：游戏内的网页由它们加载和联网，不经过 `MapleStory.exe`。
- MapleStory Classic World 的反作弊 NGS-X：`grap-core64.aes`、`grap-communicator64.aes`、`grap-updater.aes`、`NGService.exe`。
  - 它的游戏程序也叫 `MapleStory.exe`，早已在加速中。
  - 主游戏的反作弊（BlackCipher 各组件）一直在名单里，这次补上同类组件。
  - 本机 Nexon 登录器版的 MapleStory Classic World 也是这几个文件。
  - `NGService.exe` 同时是“Nexon Game Security Service”服务，启动类型为手动，不会开机常驻。

说明：以上根据目录结构判断。MSBOOST 的日志只记录规则，不记录每条连接，所以要靠实测确认。

## 3. 修改

加速名单从 24 项增加到 31 项（`internal/profiles`），原有 24 项不变：
- MapleStory 组：`MapleBrowser_WZ2.exe`
- Nexon 登录器与组件组：`ms-launcher.exe`、`DwarfAxe.exe`
- 反作弊组：`grap-core64.aes`、`grap-communicator64.aes`、`grap-updater.aes`、`NGService.exe`

名单里的进程如果不联网，就不会产生任何流量。测试会检查新增的进程都在名单中，并检查分组显示仍完整覆盖整个名单。

## 4. 实测步骤

需要一台装有 Steam 版 MapleStory 的电脑，可以请反馈问题的玩家帮忙。

### 测试 1：通过 Steam 启动（核心验收）

1. 下载测试包，解压，导入节点配置，开始加速。
2. 在 Steam 中启动 MapleStory。
3. “NEXON”登录窗口应正常显示登录界面，登录后能进入游戏。

### 测试 2：通过 Nexon 登录器启动

确认和 v1.0.1 一样正常。

### 测试 3（可选）：MapleStory Classic World

玩经典世界的话，进入游戏玩一段时间，确认不掉线、也没有反作弊提示。

### 测试 4：界面

在“游戏与加速项”页面：
- MapleStory 组显示 `MapleBrowser_WZ2.exe`；
- Nexon 登录器与组件组显示 `ms-launcher.exe`、`DwarfAxe.exe`；
- 反作弊组显示 `grap-*.aes` 三项和 `NGService.exe`。

### 如果登录窗口仍有内容加载不出来

可能是节点的域名过滤：节点会拒绝 GFW 列表中的域名（节点端 `msboost-filter` 规则，见规划 §4.3）。如果登录页面用到了这类域名，比如谷歌的人机验证，相关内容就会加载失败。

请记下卡在哪一步并截图，同时附上日志（`logs\launcher-日期.log`）。日志不要公开发布。
