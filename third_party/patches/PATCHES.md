# 对 ProxyBridge 的补丁

`scripts/prepare-core-src.ps1`（由 `scripts/build-core.ps1` 调用）按下面的步骤处理 ProxyBridge 源码，子模块本身始终保持上游原样：

1. 把 `third_party/ProxyBridge/Windows/src` 复制到 `.cache/core-src`；
2. 把换行统一成 LF，这样补丁的应用结果与 `core.autocrlf` 设置无关；
3. 在仓库根目录用 `git apply --directory=.cache/core-src` 按文件名顺序应用补丁；
4. 用反向检查确认每个补丁确实生效。

补丁文件按 LF 换行生成。

| 编号 | 文件 | 内容 | 状态 |
|---|---|---|---|
| P1 | `0001-firewall-rule-name.patch` | 防火墙入站规则从 `ProxyBridge relay <哈希>` 改为 `MSBOOST relay <哈希>`，说明文字同步修改 | 已生效（M1）。专用网络实测不需要这条规则；公用网络和 VPN 网卡需要它（待 M4 实测），因此保留 |
| P2 | — | 没有 UDP 规则时不截获 UDP | 由性能数据决定 |
| P3 | — | 导出中继端口的占用状态，或允许自定义端口 | 按需 |
| P4 | `0002-localhost-proxy-ports.patch` | 新增 `ProxyBridge_SetLocalhostProxyPorts`：列出的本机 TCP 端口不再一律直连，可以按规则代理。启动器把 Windows 系统代理（v2rayN、Clash 等）的端口传进来，游戏发往系统代理的连接就会转到启动器自己处理，而不是交给那些软件。UDP 和其他本机端口不受影响 | 已生效（v1.0.1） |
| P5 | `0003-handoff-diagnostics.patch` | 转交诊断和桥接试修（新文件 `pb_diag.c`）。发往外网的连接要“掉头”交给本地中转，补丁记录这个过程的每一步：连接请求和后续包有没有转给中转、中转的回应有没有转回程序、中转是接收了还是因为检查不通过关掉了。每类前 8 条写日志，停止时写汇总。旁听句柄（优先级最高，只复制不拦截）记录中转发出的 SYN-ACK 和 RST，以及真实服务器发给加速连接的重置。桥：主句柄的过滤条件会跳过其他驱动注入的包（impostor），桥在主句柄下方收取这类外发 TCP、UDP 包，从主句柄上方重新注入，主句柄就照常按规则处理；交回的包如果被送回来，桥自动关闭。数据包计数只统计 IPv4，中转接收的统计包括 IPv6 | 测试版（2026-10-25），找到原因后决定保留还是删除 |

规则名的哈希算法未改动，因此 `internal/redirector.FirewallRuleNames` 能同时算出自编核心和官方核心的规则名。“清理”功能会把两个都删除。
