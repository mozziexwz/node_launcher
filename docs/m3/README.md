# M3 打包与发布（便携版）

对应规划 §6 的 M3：把程序做成解压即用的便携版。zip 里只有一个 exe，加上许可文件和校验文件。

## 1. 完成内容

| 项目 | 内容 |
|---|---|
| 内嵌运行文件 | 正式版（`-tags bundle`）把 `ProxyBridgeCore.dll`、`WinDivert.dll`、`WinDivert64.sys` 编进 exe（`bundle.go`）。开发版仍从程序目录加载（`bundle_dev.go`） |
| 释放位置 | `%ProgramData%\MSBOOST\runtime\<哈希>\`。哈希取自三个文件的内容，文件相同的版本共用一个目录，其他版本的目录会自动删除。`ProgramData` 的位置通过系统已知文件夹接口取得，不读用户可以改的环境变量 |
| 目录权限 | `MSBOOST`、`runtime` 和版本目录的所有者都设为 Administrators，权限设为受保护（不继承上级），只允许 Administrators 和 SYSTEM 访问。目录如果是链接（junction、符号链接），会先删掉链接再重建 |
| 每次开始加速前 | 锁定三个文件（别人不能写入、改名或删除），校验 SHA-256；缺失或不一致就重新释放。锁一直保持到 DLL 加载完、驱动启动之后。如果文件正被别的程序以写入方式打开，开始加速会失败并提示 |
| 清理 | 删除 `%ProgramData%\MSBOOST`。本次运行加速过时，两个 DLL 仍在使用，无法立即删除，会安排在下次重启电脑时删除，并在报告里说明 |
| 第三方声明 | `scripts/notices` 根据实际编进程序的 Go 依赖（`go list -deps`），加上 Go 本身、分流核心、WinDivert 和 React，生成 `THIRD_PARTY_NOTICES.txt`。相同的许可文本只写一次，与 GPL-3.0 相同的文本指向 `LICENSE.txt` |
| 发布包 | CI 产物 `MSBOOST-Launcher-<版本>-windows-x64`，内含 `MSBOOST-Launcher.exe`、`LICENSE.txt`、`THIRD_PARTY_NOTICES.txt` 和 `SHA256SUMS.txt` |
| 正式发布 | 推送 `v*` 标签时，CI 会编出带版本号的 exe（文件属性里的版本也会更新），打成 zip，并创建一个**草稿** Release，附上 zip 和它的 `.sha256`。说明取自 [发布说明模板](../release-notes-template.md)，确认后手动发布 |
| 开源许可对话框 | 仍是组件表，组件名照用户要求显示（mieru 显示为“見える”）；并注明完整声明见 `THIRD_PARTY_NOTICES.txt`。这个文件按原样列出各组件的名称和版权声明，属于随附的法律文件，不是界面 |

### 测试

- `internal/runtimefiles`：
  - 释放后内容正确，锁定期间无法写入或改名；
  - 文件被改或被删后会修复；
  - 别人正以写入方式打开文件时，检查失败；
  - 释放目录是链接时，会换成真实目录，且不动链接指向的内容；
  - 会删除其他版本的目录；
  - 目录名随文件内容变化。
- `internal/winsys`：
  - `OpenLocked` 的锁定行为；
  - 链接识别；
  - `SecureDirectory` 设置的所有者、受保护的 DACL，以及新文件继承到的权限（需要管理员，在 CI 中运行）。
- `internal/app`：
  - 每次开始加速前都检查一次；
  - 锁一直保持到分流核心启动之后；
  - 检查失败时，不会启动任何东西。

## 2. 验收结果（2026-10-14，通过）

测试包为 CI 编出的 `MSBOOST-Launcher-dev-268858d-windows-x64`，在本机和 Windows 10 虚拟机上测试。

| 测试 | 结果 |
|---|---|
| 1 解压即用 | ✅ 解压后只有 exe 和三个 txt 文件。第一次开始加速时，日志显示释放了三个运行文件，版本为 `dev-268858d` |
| 2 释放目录和权限 | ✅ `runtime\309870af9b2dd2fb` 里有三个运行文件；`icacls` 只列出 `BUILTIN\Administrators:(OI)(CI)(F)` 和 `NT AUTHORITY\SYSTEM:(OI)(CI)(F)` |
| 3 自动修复 | ✅ 删除 `WinDivert64.sys` 后，下次开始加速时日志显示“已释放运行文件 …WinDivert64.sys”，加速正常 |
| 4 清理 | ✅ 加速过后清理，报告“有 2 个运行文件正在使用，已安排在下次重启电脑时删除”。重开程序后直接清理，报告“已删除 C:\ProgramData\MSBOOST”。之后目录、驱动服务和 `MSBOOST relay` 防火墙规则都不存在 |
| 5 干净的电脑 | ✅ Windows 10 虚拟机上解压即用 |

另外看到一点，符合预期：第一次清理（DLL 仍在使用）后接着开始加速时，被清理掉的驱动文件也被自动重新释放了。

## 3. 实测步骤

### 准备

1. 打开仓库的 Actions 页面，进入 `m3-release` 分支最新一次通过的 `ci` 运行，在页面底部下载 `MSBOOST-Launcher-dev-xxxxxxx-windows-x64`。
2. 解压到一个新文件夹，例如 `F:\AI\Claude\MSBOOST启动器\_local\m3ci\`。里面应该**只有** `MSBOOST-Launcher.exe`、`LICENSE.txt`、`THIRD_PARTY_NOTICES.txt` 和 `SHA256SUMS.txt`，没有 DLL 和 .sys 文件。
3. 退出之前版本的 MSBOOST 启动器。

### 测试 1：解压即用

1. 双击 exe 运行，导入节点配置，开始加速，进游戏玩一会儿。
2. 日志里应有三行“已释放运行文件 …”（第一次运行时）。“设置与关于 → 关于”里的版本是 `dev-xxxxxxx`。

### 测试 2：释放目录和权限

开始加速后，在管理员命令行执行：

```bat
dir C:\ProgramData\MSBOOST\runtime /s
icacls C:\ProgramData\MSBOOST
```

- 第一条应列出一个 16 位哈希名的目录，里面是三个运行文件。
- 第二条应只有 `BUILTIN\Administrators:(OI)(CI)(F)` 和 `NT AUTHORITY\SYSTEM:(OI)(CI)(F)` 两行，没有 Users 或你的用户名。

### 测试 3：自动修复

1. 停止加速。
2. 在管理员命令行删除驱动文件（文件夹名换成测试 2 里看到的哈希）：

   ```bat
   del C:\ProgramData\MSBOOST\runtime\<哈希>\WinDivert64.sys
   ```

3. 再开始加速，应正常加速，日志里出现“已释放运行文件 …WinDivert64.sys”。

### 测试 4：清理

1. 加速过之后停止，在“设置与关于”里点“清理”。报告应说明运行文件正在使用，已安排在下次重启电脑时删除。
2. 退出程序后重新打开，不要开始加速，直接点“清理”。报告应为“已删除 C:\ProgramData\MSBOOST”。
3. 在管理员命令行确认：

   ```bat
   dir C:\ProgramData\MSBOOST
   sc query WinDivert
   netsh advfirewall firewall show rule name=all | findstr /C:"MSBOOST relay"
   ```

   第一条应提示找不到文件，第二条应提示服务不存在，第三条应没有输出。

### 测试 5（可选）：干净的电脑

如果有虚拟机或另一台没装过 MSBOOST 的电脑，在那里重复测试 1，确认解压即用。

### 回报

把每项测试的结果（通过/有问题）发过来。有问题时附上截图和日志（`logs\launcher-日期.log`），日志不要公开发布。

## 4. 发布第一个版本

验收通过后，由你决定版本号（例如 `v0.1.0`）。打标签并推送后：
- CI 会生成草稿 Release；
- 你在 GitHub 上检查说明，补上“更新内容”，然后发布；
- 网站下载页要在下载链接旁写上源代码地址 https://github.com/mozziexwz/node_launcher （GPL-3.0 第 6 条）。
