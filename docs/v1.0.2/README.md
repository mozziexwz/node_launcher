# v1.0.2：修复 Steam 版登录窗口黑屏

## 1. 问题

玩家反馈：开着 MSBOOST 加速，通过 Steam 启动 MapleStory 时，弹出的“NEXON”登录窗口一直是黑屏，登录界面加载不出来。

## 2. 原因

- Steam 版不经过 Nexon 登录器。Steam 直接启动 `MapleStory.exe`，由游戏自己弹出 Nexon 账号登录窗口。
- 这个窗口是游戏目录 `appdata\NxOverlay` 里的网页组件，基于 CEF（内嵌的 Chromium）。
  - 按 Chromium 的结构，网页的显示和联网都在子进程 `DwarfAxe.exe` 里完成，不在 `MapleStory.exe` 里。
  - `DwarfAxe.exe` 的文件说明是“DirectX Web Assistant Runtime Framework”，出自 Nexon Corporation。
- `DwarfAxe.exe` 不在加速名单中，所以登录页面直连 Nexon，没有经过 MSBOOST 节点。
  - 直连时会受网络环境和地区检查影响，和 v1.0.1 修复的“所在地区不支持”是同一类问题。
  - 页面加载不出来，窗口就是黑屏。
- 通过 Nexon 登录器启动时，登录页面由 `nexon_client.exe` 加载，它在名单里，所以一直正常。

游戏目录里还有一个同类程序 `MapleBrowser_WZ2.exe`（游戏内浏览器），同样不在名单中，这次一并加入。

说明：以上是根据游戏文件结构做的判断。MSBOOST 的日志只记录规则，不记录每条连接，所以还要靠实测确认。

## 3. 修改

加速名单从 24 项增加到 26 项（`internal/profiles`）：
- MapleStory 组：增加 `MapleBrowser_WZ2.exe`；
- Nexon 登录器与组件组：增加 `DwarfAxe.exe`。

其余没有改动。测试会检查这两个进程在名单中，并检查分组显示仍完整覆盖整个名单。

## 4. 实测步骤

需要一台装有 Steam 版 MapleStory 的电脑，可以请反馈问题的玩家帮忙。

### 测试 1：通过 Steam 启动（核心验收）

1. 下载测试包，解压，导入节点配置，开始加速。
2. 在 Steam 中启动 MapleStory。
3. “NEXON”登录窗口应正常显示登录界面，登录后能进入游戏。

### 测试 2：通过 Nexon 登录器启动

确认和 v1.0.1 一样正常。

### 测试 3：界面

在“游戏与加速项”页面：
- MapleStory 组显示 `MapleBrowser_WZ2.exe`；
- Nexon 登录器与组件组显示 `DwarfAxe.exe`。

### 如果登录窗口仍有内容加载不出来

可能是节点的域名过滤：节点会拒绝 GFW 列表中的域名（节点端 `msboost-filter` 规则，见规划 §4.3）。如果登录页面用到了这类域名，比如谷歌的人机验证，相关内容就会加载失败。

请记下卡在哪一步并截图，同时附上日志（`logs\launcher-日期.log`）。日志不要公开发布。
