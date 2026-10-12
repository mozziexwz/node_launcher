# MSBOOST 启动器（node_launcher）

把 mieru 客户端和按进程分流合并成一个带界面的 Windows 程序：导入节点配置 → 开始加速 → 打开冒险岛。

- 只代理冒险岛相关进程，其他软件直连。
- 不改路由表、DNS 和系统代理，也不装虚拟网卡。

> **状态：M0 主要验证已完成（结果见 [docs/m0/README.md](docs/m0/README.md)），下一步 M1；尚不可用。** 规划见 [docs/MSBOOST一体化启动器开发规划.md](docs/MSBOOST一体化启动器开发规划.md)。

## 组成

| 组件 | 版本 | 用途 |
|---|---|---|
| [mieru](https://github.com/enfein/mieru) | v3.38.1 | 进程内嵌入，提供本地 SOCKS5（只监听 127.0.0.1，端口随机，每次启动生成会话凭据） |
| [ProxyBridge](https://github.com/InterceptSuite/ProxyBridge) 核心 | v4.1.0 | 编译为 `ProxyBridgeCore.dll`，基于 WinDivert 把指定进程的流量转进本地 SOCKS5 |
| [WinDivert](https://github.com/basil00/WinDivert) | 2.2.2-A | 官方签名的驱动与库，原样分发 |
| [Wails](https://wails.io) v2 + React/TypeScript | — | 界面（M0 搭建） |

## 目录

```text
main.go, app.go, tray.go   Wails 界面入口、前端绑定、托盘
frontend/      界面（React + TypeScript + Vite）
internal/      engine（嵌入 mieru）、redirector（ProxyBridgeCore.dll）、probe（网络检测）、profiles、winsys
cmd/m0spike/   M0 验证工具（命令行）
docs/          规划与设计文档；全程测速 v2 合同；M0 验证指南（docs/m0/）
legacy/        旧版 BAT 启动器与 PowerShell 测试，仅供移植参考，不参与构建
scripts/       获取 WinDivert、编译 ProxyBridgeCore.dll 等脚本
third_party/   ProxyBridge 子模块（固定 v4.1.0），WinDivert 校验值
```

构建界面：安装 Wails CLI v2.16.0（`go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`），然后在仓库根目录执行 `wails build`，产物为 `build/bin/MSBOOST-Launcher.exe`。

## 开发环境

- Windows 10/11 x64
- Go 1.27.1、Node.js 24
- Visual Studio 2022 Build Tools（“使用 C++ 的桌面开发”），用于编译 `ProxyBridgeCore.dll`

```powershell
git clone --recurse-submodules https://github.com/mozziexwz/node_launcher.git
cd node_launcher
.\scripts\fetch-windivert.ps1   # 下载并校验 WinDivert 2.2.2-A
.\scripts\build-core.ps1        # 编译 ProxyBridgeCore.dll（需要 MSVC）
```

## 许可

本项目使用 [GPL-3.0](LICENSE) 许可。第三方组件及其许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

本项目与 ProxyBridge / InterceptSuite、mieru、WinDivert 的作者没有隶属关系，感谢这些项目。
