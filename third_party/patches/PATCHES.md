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
| P5 | `0003-impostor-bridge.patch` | 兼容会转发数据包的网络驱动（新文件 `pb_bridge.c`）。主句柄的过滤条件会跳过其他驱动注入的包（impostor），有的电脑上已建立连接的包都会被转发，转交因此失败。桥在主句柄下方（优先级 122）收取外发的 impostor TCP、UDP 包，从主句柄上方（124）重新注入，主句柄就照常按规则处理。同一个包如果带着更低的 TTL 又被收回来，桥自动关闭。另记转交过程的计数，停止时写两行汇总（`Hand-off summary`、`Bridge summary`）。转交的数据包计数只统计 IPv4，中转接收的统计包括 IPv6 | v1.1.1。测试版 dev-a634de4 已在出问题的电脑上验证（2026-10-25） |

规则名的哈希算法未改动，因此 `internal/redirector.FirewallRuleNames` 能同时算出自编核心和官方核心的规则名。“清理”功能会把两个都删除。
