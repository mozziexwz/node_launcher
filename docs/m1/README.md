# M1 核心引擎

对应规划 §6 的 M1：不含界面，完成核心编排。正式界面（M2）和命令行 `msboostctl` 共用同一套代码（`internal/app`）。

## 1. 完成内容

| 模块 | 内容 | 测试 |
|---|---|---|
| `internal/probe` | 网络检测（出口 IP、到中转、全程 v2） | 旧 BAT 的 64 个边界用例、20 组行为用例中的前 16 组，全部移植并通过；与节点端 `tcp_probe.py` 的契约测试通过 |
| `internal/app` | 启动/停止状态机（任一步失败都逆序回滚）、Discord/Steam 开关可在加速中切换、网络检测、清理 | 9 个失败点的回滚测试；检测页的 4 组移植用例（第 17–20 组） |
| `internal/store` | 便携数据：`configs\`、`logs\`、`data\`、`settings.json`；从旧 BAT 文件夹迁移 | 设置校验、损坏回退、导入、文件名防越权、迁移 |
| `internal/logx` | `[程序]`、`[核心]`、`[分流]` 标签，按天分文件、保留 7 天，写盘前遮盖密码 | 遮盖、轮换、保留期、订阅 |
| `internal/winsys` | 冲突检测、单实例锁、删除防火墙规则 | 冲突规则、单实例 |
| 补丁 P1 | 防火墙规则改名为 `MSBOOST relay <哈希>` | 应用后反向校验；CI 编译 |
| `cmd/msboostctl` | 开发与验收用命令行 | 本机实测：经日本中转检测成功（到中转 54 ms、到游戏 151 ms） |

与旧 BAT 行为不同的一处：配置里的 `socks5Authentication` 不再阻止全程测速。程序始终使用自己的会话凭据（见 `TestCheckIgnoresConfiguredLocalAuthentication`）。

## 2. 请你实测（M1 退出标准）

用 CI 编出来的测试包，在真实冒险岛上跑一遍完整加速。测试包里的分流核心是我们自己编译、已打补丁 P1 的版本。

### 准备

1. 打开仓库的 Actions 页面，进入 `m1-core` 分支最新一次通过的 `ci` 运行，在页面底部下载 `m1-windows-x64`。
2. 解压到一个新文件夹，例如 `F:\AI\Claude\MSBOOST启动器\_local\m1ci\`。里面应有 `msboostctl.exe`、`ProxyBridgeCore.dll`、`WinDivert.dll`、`WinDivert64.sys`、`SHA256SUMS.txt`。
3. 退出冒险岛；确认官方 ProxyBridge 没有在运行（`sc stop ProxyBridgeSvc`）。
4. 打开“以管理员身份运行”的命令行，然后执行：

   ```bat
   cd /d F:\AI\Claude\MSBOOST启动器\_local\m1ci
   msboostctl import-legacy ..\configs
   msboostctl configs
   msboostctl status
   ```

   `import-legacy` 会把 `_local\configs` 里的配置导入到本文件夹的 `configs\`。`status` 中应显示“冲突检测：无”。

### 测试 1：网络检测

```bat
msboostctl use 日本中转.json
msboostctl check
```

### 测试 2：完整加速（核心验收）

```bat
msboostctl run
```

看到“加速已开始”后打开冒险岛：登录、进频道、换频道、进商城，玩 30 分钟以上，然后按 `Ctrl+C`。日志会自动写入 `logs\launcher-日期.log`。

### 测试 3：加速中切换 Discord（可选）

在另一个管理员命令行窗口中运行同一个程序，会提示“另一个 MSBOOST 启动器正在运行”，这是单实例锁在起作用。

开关的“加速中切换”效果要等 M2 界面才方便测。现在可以先用 `msboostctl addon discord on` 保存开关，下次 `run` 时生效。

### 测试 4：防火墙规则是否必要（对应 M0 第 6 项，可选）

1. 先运行 `msboostctl run`，进游戏确认正常，再退出游戏（加速保持运行）。
2. 在另一个管理员命令行窗口查看规则名，然后删除它：

   ```bat
   netsh advfirewall firewall show rule name=all | findstr "MSBOOST relay"
   netsh advfirewall firewall delete rule name="MSBOOST relay xxxxxxxx"
   ```

3. 重新打开冒险岛，看能否正常登录、进频道。

核心只在启动加速时检查一次这条规则，所以运行中删除后，新建的连接就是在“没有规则”的条件下测试的。结果请告诉我，它决定补丁 P1 是保留规则还是去掉规则。

### 结束后

- 执行 `msboostctl clean`，确认它会删除驱动服务和 `MSBOOST relay` 防火墙规则。
- 把 `_local\m1ci\logs\` 下的日志发给我。日志里有节点地址，不要公开发布。
