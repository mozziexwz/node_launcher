# M0 技术验证

对应规划 §6 的 M0。验证工具是 `cmd/m0spike`（命令行），M1 之后由正式程序取代。

## 1. 结果（2026-10-13）

| 规划项 | 结果 | 依据 |
|---|---|---|
| 1 编译 `ProxyBridgeCore.dll` | ✅ CI 用 MSVC 从子模块源码编译通过，`TestCoreABI` 在自编 DLL 上通过 | CI 运行 #1、#2。自编 DLL 的真实游戏测试待补（见 §2） |
| 2 Go 不用 cgo 调用 DLL，原生线程回调 | ✅ | 连续运行 4 小时 58 分，约 7.3 万次连接进程查询，日志零丢失；`-conns` 测试 3 分钟内收到数百条连接事件，零丢失 |
| 3 嵌入的核心连真实节点 | ✅ | 日本中转配置：出口 IPv4 查询成功；到中转 53 ms、到游戏 147 ms，均为 3/3 成功 |
| 4 冒险岛实测 | ✅ | 日本中转连续加速 4 小时 58 分，累计 975 条加速连接，游戏期间稳定保持 7 条；MapleLegends 登录（8484）、频道（7579）以及 `nexon_client.exe` 都走了加速 |
| 5 停止后驱动状态 | ✅ | 每次停止后 WinDivert 都是 STOPPED；之后服务项被系统自动删除（下次启动前为 NOT_INSTALLED） |
| 6 防火墙规则是否必要 | 待 M1 | 需要我们自己编译、打了补丁 P1 的 DLL；官方 DLL 每次启动都会重新添加该规则 |
| 7 性能基线 | 未测（可选） | 移到 M4 验收 |
| 8 Wails 以管理员运行 | ✅ | 窗口和文件选择框正常。文件类型的文字已按要求改为“MSBOOST 节点配置” |
| 9 托盘图标 | 部分 ✅ | 关闭窗口后程序留在托盘继续运行；托盘菜单、单实例、退出后图标消失待确认（§2） |
| 10 UDP 经嵌入的核心 | ✅ | 带会话凭据的 UDP ASSOCIATE 建立成功，断线后自动重建。这次冒险岛没有产生 UDP 流量，可用 Discord 语音实测（§2，可选） |
| 只代理冒险岛（规划 §7.4） | ✅ | `-conns` 日志中只有冒险岛相关进程是 Proxy；浏览器、Telegram、Discord、xray、DNS 等全部 Direct，本程序连节点的连接也是 Direct |

### 观察到的问题

- 21:31，一次到 `g.nexonstatic.com:443` 的连接被节点拒绝。
  - 开启“发送域名”时由节点解析，节点规则 `msboost-filter`（GFW 规则集）会拒绝其中的域名；也可能是偶发失败。游戏不受影响。
- 02:13，到节点的会话断了一次（UDP 控制连接被关闭），其间一个 CONNECT 失败，约 5 秒后自动恢复，之后活动连接数仍是 7。
  - 以后可以在界面上加“节点连接中断/已恢复”的提示。

### 2026-10-13 的变更

- 核心升级到 mieru **3.38.1**。它修复了 SOCKS5 认证漏洞：
  - 3.38.0 在客户端同时提出“无认证”和“用户名密码”两种方式时，会放行“无认证”；
  - 新增的回归测试 `TestEngineNeverFallsBackToNoAuth` 锁住了这一点。
- 用户可见的文字统一显示 MSBOOST。核心库自己的日志加 `[核心]` 前缀，分流日志加 `[分流]` 前缀。
- 新增 Discord、Steam 两个可选加速项：`m0spike run -discord`、`-steam`。
- `-core` 指向的文件不存在时，给出明确提示。

## 2. 还可以补做的测试（可选）

`_local\m0\m0spike.exe` 已用新版本重新编译。下面的测试都在“以管理员身份运行”的命令行中执行，并先进入 `cd /d F:\AI\Claude\MSBOOST启动器\_local\m0`。

### 用我们自己编译的 DLL 再跑一次（对应第 1 项）

1. 打开 GitHub 仓库的 Actions 页面，进入最新一次通过的 `ci` 运行，在页面底部的 Artifacts 下载 `m0-windows-x64`（需要登录 GitHub）。
2. 把压缩包解压到 `F:\AI\Claude\MSBOOST启动器\_local\ci\`，确认存在 `_local\ci\core\ProxyBridgeCore.dll`。
3. 执行：

   ```bat
   m0spike run -config ..\configs\直连.json -core ..\ci\core\ProxyBridgeCore.dll
   ```

### 托盘（对应第 9 项）

1. 右键托盘图标，在菜单里点“开始加速”“停止加速”，窗口中的状态应同步变化。
2. 程序运行时再双击一次 exe：不会开第二个，而是把现有窗口调到前面。
3. 托盘菜单选“退出”：程序退出，托盘图标消失。

你上次打开的 `MSBOOST-Launcher.exe` 目前还在托盘里运行，测试时可以直接用它，最后用“退出”关闭。

### Discord 语音走加速（对应第 10 项，验证真实 UDP 流量）

```bat
m0spike run -config ..\configs\直连.json -discord -conns -duration 5m
```

期间进入一个 Discord 语音频道说几句话。日志中应出现 `Discord.exe ... Proxy` 的 UDP 连接，语音正常。

### 结束后

- 把新生成的 `m0spike-*.log` 发给我（在 `_local\m0\`）。日志里有节点地址，不要公开发布。
- 防火墙规则清理命令（规则名以 `m0spike svc` 的输出为准）：

  ```bat
  netsh advfirewall firewall delete rule name="ProxyBridge relay 1c178a39"
  ```

## 3. 测试命令参考

| 命令 | 用途 |
|---|---|
| `m0spike svc` | 查看权限、服务状态、中继端口、防火墙规则名（无需管理员） |
| `m0spike probe -config 配置.json [-game IP:端口]` | 只启动核心，执行三项网络检测（无需管理员） |
| `m0spike run -config 配置.json [-core DLL] [-conns] [-discord] [-steam] [-duration 30m]` | 完整加速（需要管理员） |

做加速测试前需要先关闭官方 ProxyBridge：在管理员命令行执行 `sc stop ProxyBridgeSvc`。
