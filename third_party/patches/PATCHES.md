# 对 ProxyBridge 的补丁

`scripts/build-core.ps1` 会先把 `third_party/ProxyBridge/Windows/src` 复制到 `.cache/core-src`，再按文件名顺序对副本执行 `git apply`。子模块本身始终保持上游原样。

| 编号 | 文件 | 内容 | 状态 |
|---|---|---|---|
| P1 | — | 防火墙规则改名为 `MSBOOST relay <哈希>`（M0 第 6 项确认是否需要该规则） | 待 M1 |
| P2 | — | 没有 UDP 规则时不截获 UDP | 由 M0 性能数据决定 |
| P3 | — | 导出中继端口的占用状态，或允许自定义端口 | 按需 |

当前没有生效的补丁，编译结果等同上游 v4.1.0。
