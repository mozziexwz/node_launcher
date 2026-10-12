# 第三方组件与许可

本项目整体以 GPL-3.0 发布。下列第三方组件按各自许可使用。Go 与前端依赖的完整清单将在 M3（打包发布）时由工具生成并补充到本文件。

## mieru

- 来源：<https://github.com/enfein/mieru>
- 版本：v3.38.0
- 许可：GPL-3.0
- 使用方式：作为 Go 模块编译进程序（客户端多路复用与本地 SOCKS5 服务端）。

## ProxyBridge

- 来源：<https://github.com/InterceptSuite/ProxyBridge>
- 版本：v4.1.0（commit `c9646aaa5991aecb2559141a4f944b82513d2e30`）
- 许可：MIT
- 使用方式：Windows 核心源码以 git 子模块引入（`third_party/ProxyBridge`），编译为 `ProxyBridgeCore.dll`；对其所做的修改记录在 `third_party/patches/`。

```text
MIT License

Copyright (c) 2025 Anof-cyber/InterceptSuite

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## 界面相关组件

| 组件 | 版本 | 许可 | 来源 |
|---|---|---|---|
| Wails | v2.16.0 | MIT | <https://github.com/wailsapp/wails> |
| fyne.io/systray | v1.12.2 | Apache-2.0 | <https://github.com/fyne-io/systray> |
| React / React DOM | 19.x | MIT | <https://github.com/facebook/react> |
| Vite | 7.x | MIT | <https://github.com/vitejs/vite> |

`build/windows/icon.ico` 与 `build/appicon.png` 目前是 Wails 模板自带的占位图标，正式发布前替换为 MSBOOST 图标。

## WinDivert

- 来源：<https://github.com/basil00/WinDivert>
- 版本：2.2.2-A
- 许可：LGPL-3.0 与 GPL-2.0 双许可，本项目按 LGPL-3.0 使用
- 使用方式：分发官方发布包中未经修改的 `WinDivert.dll` 与 `WinDivert64.sys`（已由作者签名）。发布包 SHA-256 记录在 `third_party/windivert.sha256`；对应源码可从上游获取。
