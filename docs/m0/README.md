# M0 技术验证

对应规划 §6 的 M0。验证工具是 `cmd/m0spike`（命令行），M1 之后由正式程序取代。

## 1. 已在开发机上完成的验证

| 规划项 | 内容 | 结果 |
|---|---|---|
| 第 1 项 | 下载 WinDivert 2.2.2-A 并核对 SHA-256 | ✅ 与固定值一致；你本机 ProxyBridge 自带的 `WinDivert.dll`、`WinDivert64.sys` 与官方包逐字节相同 |
| 第 1 项 | 用 MSVC 编译 `ProxyBridgeCore.dll` | 本机没有 MSVC，改由 CI（GitHub Actions）编译。本地行为测试暂用你本机官方 4.1.0 的 DLL（作者签名有效），源码版本与子模块相同 |
| 第 2 项（接口部分） | Go 不用 cgo，直接调用 DLL | ✅ `AddProxyConfig`、`AddRule`、`DeleteRule`、`DeleteProxyConfig` 的参数与返回值正确，日志回调完整回传（`TestCoreABI`）。原生线程上的回调压力待测试 B 验证 |
| 第 3 项（本地部分） | 嵌入 mieru 3.38.0，本地 SOCKS5 只监听回环、端口随机、每次生成新凭据 | ✅ 错误密码会被拒绝，关闭后端口不再可连（`TestEngineServesLoopbackSOCKS5WithSessionCredentials`） |
| — | 全程测速 v2 客户端移植 | ✅ 首批测试通过：暖机不计时、有效失败沿用原会话、协议错误/超时后重连、旧节点不重试、多余数据、认证失败、代理未启动、参数校验、23 种损坏回复、4096 字节边界。BAT 的全部 64 + 20 组用例在 M1 补齐 |

## 2. 需要你在本机执行的测试

### 准备

1. 把节点配置（例如 `直连.json`）复制到 `_local\configs\`。该目录已加入 `.gitignore`，不会提交。
2. 退出冒险岛，关闭官方 ProxyBridge 窗口。
3. 打开“以管理员身份运行”的命令行，依次执行：

   ```bat
   sc stop ProxyBridgeSvc
   cd /d F:\AI\Claude\MSBOOST启动器\_local\m0
   m0spike svc
   ```

   `svc` 的输出中，`ProxyBridgeSvc` 应为 `STOPPED`，并且中继端口显示“空闲”。

### 测试 A：嵌入的 mieru 是否正常（对应第 3 项，无需管理员）

```bat
m0spike probe -config ..\configs\直连.json
```

预期：输出三项结果，与旧 BAT 菜单 [3] 在同一量级，出口 IPv4 一致。

### 测试 B：完整加速（对应第 2、4、5、10 项，需要管理员）

```bat
m0spike run -config ..\configs\直连.json
```

1. 看到“加速已开始”后，打开冒险岛。
2. 依次完成：登录、进频道、换频道、进商城、组队，然后保持游戏 30 分钟以上。
3. 期间每 10 秒会打印一行 `[状态]`，其中“经加速的 SOCKS5 连接”应大于 0。
4. 结束时先退出游戏，再在命令行窗口按 `Ctrl+C`，等待输出“M0 结果摘要”。

如果想无人值守跑 1 小时，看原生线程回调是否稳定，可以加 `-duration 1h`。

### 测试 C：只代理冒险岛（可选，对应 §7.4）

```bat
m0spike run -config ..\configs\直连.json -conns -duration 3m
```

运行期间用浏览器打开几个网站。日志 `[连接]` 行中，浏览器的连接应是 DIRECT，冒险岛相关进程的连接应是 PROXY。

### 测试 D：性能基线（可选，对应第 7 项）

分别在“测试 B 运行中”和“已停止”两种状态下，用测速网站各测一次下载和上传速度，记录两组数字。

### 测试 E：界面与托盘（对应第 8、9 项）

界面目前只是外壳：“加速”按钮是**模拟**的，不会真的分流；“选择配置文件”会用正式的解析器校验你的配置。

1. 双击 `build\bin\MSBOOST-Launcher.exe`，在 UAC 弹窗中点“是”。程序未签名，如果 SmartScreen 拦截，点“更多信息 → 仍要运行”。
2. 窗口中“管理员权限”应显示“是”。
3. 点“选择配置文件…”，选择 `直连.json`：应显示方案名和入口地址，不显示密码。
4. 点“开始加速”，状态变为“加速中（模拟）”。右键托盘图标，菜单项应同步变为“停止加速”；在托盘菜单里切换，窗口里的状态也应跟着变。
5. 点窗口右上角的 ×：窗口隐藏，托盘图标仍在。左键单击托盘图标，窗口重新出现。
6. 程序运行时再双击一次 exe：不会开第二个，而是把现有窗口调到前面。
7. 托盘菜单选“退出”：程序退出，托盘图标消失。把鼠标移到托盘区，确认没有残留的“幽灵图标”。

### 结束后

- 把 `_local\m0\` 下生成的 `m0spike-*.log` 发给我。日志里可能有节点地址，不要公开发布。
- 恢复官方 ProxyBridge：`sc start ProxyBridgeSvc`。
- 官方核心会为 `m0spike.exe` 添加一条防火墙入站规则。规则名以 `m0spike svc` 的输出为准，exe 路径变了名字也会变。清理命令：

  ```bat
  netsh advfirewall firewall delete rule name="ProxyBridge relay 1c178a39"
  ```

## 3. 结果记录

| 规划项 | 结果 | 备注 |
|---|---|---|
| 1 编译 DLL | ✅ CI 运行 #1 用 MSVC 编译通过，`TestCoreABI` 在自编 DLL 上通过 | 本机仍无 MSVC，本地行为测试用官方 DLL |
| 2 Go 调用 DLL、原生线程回调 | 接口 ✅；压力待测试 B | |
| 3 嵌入 mieru | 本地 ✅；真实节点待测试 A | |
| 4 冒险岛实测 | 待测试 B | |
| 5 停止后驱动状态 | 待测试 B 摘要 | |
| 6 防火墙规则是否必要 | 需要我们自己编译的 DLL（补丁 P1）后再测 | 官方 DLL 每次启动都会重新添加该规则，无法关闭 |
| 7 性能基线 | 待测试 D | |
| 8 Wails 管理员运行 | 已构建（Wails v2.16.0，exe 内嵌 requireAdministrator 清单）；待测试 E | |
| 9 托盘图标 | 已实现（`fyne.io/systray` v1.12.2，在独立锁定线程上运行）；待测试 E | |
| 10 UDP 经嵌入的 mieru | 待测试 B（规则为 BOTH） | |
