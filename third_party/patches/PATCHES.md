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

规则名的哈希算法未改动，因此 `internal/redirector.FirewallRuleNames` 能同时算出自编核心和官方核心的规则名。“清理”功能会把两个都删除。
