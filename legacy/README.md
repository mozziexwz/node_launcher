# 旧版参考文件

这里是旧版 BAT 启动器及其 PowerShell 测试，仅作为移植参考和测试用例来源，**不参与构建，也不能直接运行**（BAT 需要同目录下的 mieru 客户端 `msboost.exe` 和节点配置）。

| 文件 | 说明 |
|---|---|
| `MSBOOST启动器_全程TCP测速.bat` | 旧版启动器，与原件逐字节一致（UTF-8 无 BOM、CRLF） |
| `tests/Test-FullPathBoundaries.ps1` | 全程测速 v2 回复解析的 64 个边界用例，M1 移植为 Go 测试 |
| `tests/Test-FullPathLatency.ps1` | 全程测速 v2 的 20 组行为用例，M1 移植为 Go 测试 |

BAT 打包与冒烟检查（`Test-FullPathPackaging.ps1`、`Test-FullPathSmoke.ps1`）和 v1 测速方案的测试（`Test-Latency.ps1`、`Test-RelayTcpProbe.ps1`）与现在的程序无关，已删除，需要时可从 git 历史中找回。

## 与原件的差异

测试中出现的两个真实节点 IP 已替换为 RFC 5737 文档保留地址，避免在公开仓库中暴露节点：

| 原值 | 替换为 |
|---|---|
| 节点 A | `192.0.2.10` |
| 节点 B | `192.0.2.20` |

`docs/MSBOOST全程TCP测速整改计划.md` 中的节点 IP 也做了同样的替换。游戏服务器地址（如 `35.155.204.207`、`51.222.56.192`）是公开地址，保持不变。
