# MSBOOST UDP 传输整改计划

> 日期：2026-10-30
> 涉及：MSBOOST 项目（MSBOOST.DE）的节点安装脚本、执行机、网站和中转；MSBOOST 启动器（MSBOOST-Launcher.exe）；MSBOOST 本地版
> 代码基准：MSBOOST.DE `3e00d8f`，启动器 `133edcd`；启动器内嵌 mieru v3.39.0，节点用 mihomo v1.19.33（内置 mieru v3.38.1）。文中行号以这些版本为准，mieru 的行号按 v3.39.0
> 状态：第一阶段已在 `feat/udp-transport` 分支实施（第 10 节），节点、执行机和启动器（含开始加速和退回 TCP）已真机测试（10.4）；不做晚高峰对比（10.1）；第二阶段已在 MSBOOST.DE 的 `feat/relay-udp` 分支实施（11.7），自备中转、Agent 和两跳加前置机的受管线路已真机测试（11.8）；发版、本地版未做

## 1. 结论

- 部署节点时不让用户选 TCP 还是 UDP。新节点在同一个端口上同时开 TCP 和 UDP。
- 节点开了 UDP，`直连.json` 就同时写 TCP 和 UDP 两个绑定（同端口，TCP 在前）；没开就只写 TCP。配置本身就说明节点支不支持 UDP。
- 启动器只在配置里有 UDP 绑定时才让用户选连接方式，默认 TCP。启动时只保留选中的那个绑定；选 UDP 时先预检，失败就退回 TCP。
- `applyToAllUDPPacket` 在节点和客户端配置里都显式写 `true`。
- 中转暂时只支持 TCP，网站生成的中转配置只写 TCP。中转支持 UDP 放到第二阶段。
- 客户端只有 MSBOOST-Launcher.exe。旧版 BAT 和 MSBOOST.exe 已淘汰，本计划不考虑它们。

曾考虑过另一种做法：配置只写 TCP，由启动器在内存里换成同端口的 UDP。不采用，因为启动器无从知道节点支不支持 UDP，在中转和未升级的节点上选 UDP 只能等预检失败。

## 2. 配置格式

### 2.1 直连.json

节点开了 UDP 时，`servers` 和 `trafficPattern` 写成：

```json
"servers": [
  {
    "ipAddress": "203.0.113.10",
    "domainName": "",
    "portBindings": [
      { "port": 34567, "protocol": "TCP" },
      { "port": 34567, "protocol": "UDP" }
    ]
  }
],
"mtu": 1400,
"trafficPattern": {
  "nonce": { "applyToAllUDPPacket": true }
}
```

开了低熵时，`trafficPattern` 里照旧还有 `lowEntropy`。

规则：
- UDP 绑定和 TCP 绑定端口相同。网站和执行机只需记一个端口，防火墙、端口检查、中转改写都按这一个端口处理。
- TCP 绑定排第一。现有按 `portBindings[0]` 读端口的代码不受影响。
- 只有节点确实在监听 UDP 时才写 UDP 绑定。
- 公网 UDP 检查不通过时（4.2）不去掉 UDP 绑定：用户在安全组放行后不用重新下载配置，启动器的预检会兜底。
- mieru 接受这种写法：同一端口的 TCP 和 UDP 分开登记，不算重复（mieru `pkg/appctl/appctlcommon/port_binding.go` 的 `FlatPortBindings`）。

### 2.2 为什么启动时只能留一个绑定

mieru 客户端每建一条底层连接，都从所有绑定里随机挑一个（mieru `pkg/protocol/mux.go:689`）。两个绑定都留着，TCP 和 UDP 会混着用；网络挡 UDP 时，大约一半连接会失败。所以启动器启动前要去掉没选中的绑定。

### 2.3 v1.1.2 及更早的启动器

旧版本没有这一步筛选，拿到双绑定配置也会混用。启动器没有自动更新，所以：
1. 先发支持 UDP 的启动器；
2. 再让安装脚本在配置里写 UDP 绑定；
3. 网站提供配置下载的地方提示 UDP 需要新版启动器（版本号发版时确定），发布说明里也写明。

## 3. applyToAllUDPPacket

它是 `trafficPattern.nonce` 下的字段，只影响 UDP（mieru `pkg/cipher/cipher.go:305-309`）：
- `false`：可打印的 nonce 前缀只用在每条底层连接的第一个 UDP 包上，后面的包 nonce 完全随机；
- `true`：每个 UDP 包都带前缀。

它不是开启 UDP 的开关，两端也不需要一致：每一端只决定自己发出的包，接收端两种都认。

现状：节点和客户端都没写这个字段，由 mieru 按主机名的哈希五五开地决定（mieru `apis/trafficpattern/config.go:167`）。每台电脑、每台 VPS 都可能不一样，出了问题很难复现。

建议两端都写 `true`：
- 每个 UDP 包的开头都一样带前缀。中间设备的 UDP 会话表超时后，会把后来的某个包当成新会话的第一个包；写 `false` 时这个包的 nonce 完全随机，和真正的第一个包不一样。
- 代价很小：nonce 有 24 字节（XChaCha20-Poly1305），前缀最多替换前 12 字节，剩下的随机性足够。
- 只写这一个字段。nonce 类型、长度、填充等其他字段仍由 mieru 自动生成，而且每个字段单独判断、单独生成（`config.go:148-188`），所以 TCP 行为不变。

没有公开资料能说明 `true` 和 `false` 哪个更不容易被 GFW 识别。国内用 UDP 最大的问题是运营商对境外 UDP 限速或丢包，这个字段解决不了。

节点端 mihomo 的 `traffic-pattern` 取值（TrafficPattern 的 protobuf 编码再转 base64，已逐字节核对）：

| 低熵档位 | 现在 | 改成 |
|---|---|---|
| 关 | 不写 | `IgIQAQ==` |
| 56 | `MgIIBA==` | `IgIQATICCAQ=` |
| 48 | `MgIIAw==` | `IgIQATICCAM=` |
| 40 | `MgIIAg==` | `IgIQATICCAI=` |
| 32 | `MgIIAQ==` | `IgIQATICCAE=` |

`IgIQAQ==` 解码是 `22 02 10 01`：nonce（字段 4）里的 applyToAllUDPPacket（字段 2）为 true。后面的 `32 02 08 0x` 就是原来的 lowEntropy（字段 6）。

客户端：`直连.json` 里写 `"nonce": {"applyToAllUDPPacket": true}`（见 2.1）。启动器不改写这个字段，以配置为准。

## 4. 第一阶段：直连节点

### 4.1 节点安装脚本（MSBOOST.DE `installers/node/msboost.sh`）

| 项 | 位置 | 现在 | 改成 |
|---|---|---|---|
| 服务端监听 | `write_server_config`，:900–938 | 只有 `msboost-in`，`transport: TCP` | 同端口再加 `msboost-in-udp`，`transport: UDP`，用户和 `traffic-pattern` 与 TCP 监听相同。mihomo 的一个 mieru 监听只能选 TCP 或 UDP 中的一种 |
| traffic-pattern | `low_entropy_pattern`，:270；`TRAFFIC_PATTERN`，:350 | 只有开低熵时才写 | 一律写，取值见第 3 节 |
| 选端口 | `port_in_use`，:584；`random_free_port`，:599 | `ss -H -ltn`，只看 TCP | 随机选端口时要求 TCP 和 UDP 都空闲（UDP 用 `ss -H -lun`） |
| 端口被占 | 停掉旧服务后的检查，:1799–1821 | 只查 TCP | 再查 UDP。UDP 被其他程序占用时只开 TCP，打印警告，配置里不写 UDP 绑定，安装不失败 |
| 启动后检查 | :1850–1855 | 服务在运行，TCP 端口在监听 | 开了 UDP 时，再确认 UDP 端口已绑定 |
| 客户端配置 | `write_client_config`，:1461–1496 | 一个 TCP 绑定；只有开低熵时才写 `trafficPattern` | 按 2.1 写 |
| 自测 | `write_selftest_config`，:1969–1995；`probe_hello`，:2016 | mihomo 出站 `transport: TCP`，跑一次 curl 和一次探针 hello | 开了 UDP 时，再用 `transport: UDP` 的出站跑一次探针 hello。出站沿用 `TRAFFIC_PATTERN`，与客户端配置一致 |
| 防火墙 | `configure_firewall`，:2022–2156；回滚，:107–131 | ufw/firewalld 只放行 `/tcp`，并记录规则是不是本脚本加的 | 同时放行 `/udp`。UDP 规则的归属单独记录：升级老节点时 TCP 规则可能早就有，UDP 规则是新加的。回滚和换端口时一并处理 |
| install.env | :949 起 | 没有 UDP 相关项 | 增加 `UDP`（1 或 0），以及 `FIREWALL_UFW_UDP_OWNED`、`FIREWALLD_RUNTIME_UDP_OWNED`、`FIREWALLD_PERMANENT_UDP_OWNED` |
| 输出 | :2165、:2178 | 写 `(TCP)`；提示安全组放行 TCP | 写 TCP+UDP；提示安全组同时放行 TCP 和 UDP |

主服务 systemd 单元的 `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK`（:997）已经允许 UDP，不用改。

### 4.2 执行机（MSBOOST.DE `internal/executor`）

- `validation.go:330` `ParseClientConfig`：接受“一个 TCP 绑定”和“TCP 加同端口 UDP”两种；`ConfigInfo` 增加 `UDP bool`。只有 UDP、两个端口不同、端口范围等仍然拒绝。:367 的错误提示“MSBOOST 自备中转仅接受 TCP 配置”跟着改。
- `validation.go:397` `RewriteClientConfig`：现在的调用都是把配置改成指向中转入口（`control/relay.go:1389`、`control/relay_front.go:80`、`executor/engine.go:331`、`control/relay_verify_routes.go:413`）。第一阶段中转不支持 UDP，所以输出只保留 TCP 绑定。
- 公网 UDP 检查：`types.go:123` 的 `Health` 增加 `PublicUDP`。配置带 UDP 时，执行机用 mihomo 以 `transport: UDP` 连节点的公网端口，经通道向节点的 `127.0.0.1:20424` 发一次 hello，成功记为可达。UDP 没有握手，只能这样测；这也是唯一能发现安全组没放行 UDP 的地方。和 `PublicTCP` 一样只做提示，不影响部署成败。要加的地方：部署 `engine.go:187`，升级 `node_upgrade.go:118`，读取配置 `node_upgrade.go:76`。
- `verify_mihomo.go:145`：`"transport": "TCP"` 改成参数，供上面的 UDP 检查使用。
- `cleanup.go:48`：防火墙记录的正则只认 `/tcp`，加上 `/udp`。`cleanup.go:272` 读取 install.env 的地方加上三项 UDP 规则归属。
- 部署结果文字（`engine.go:185`）写上 UDP。升级结果（`node_upgrade.go:116`）现在是“配置没有变化”；升级后多了 UDP 绑定时，改成“入口和认证没有变化，配置增加了 UDP”，并提醒重新下载配置。
- 老节点：用现有的“节点升级”重装一次就会加上 UDP。升级要求入口和认证不变（`node_upgrade.go:112`），多一个 UDP 绑定不违反这条。用户手里的旧配置照样能用，只是没有 UDP。

### 4.3 网站前端（MSBOOST.DE `apps/web`）

- `src/tools.tsx:541–549`：上传配置时要求只有一个端口绑定，改成也接受“TCP 加同端口 UDP”。
- `src/tools.tsx:1857–1860`：导入本机配置时取第一个绑定并要求它是 TCP，改成按协议找 TCP 绑定。
- `src/tools.tsx:643`、`:767`：检查结果加一行“公网 UDP 可达性”。`:777` 的“TCP 可达不代表游戏可正常使用”改成不特指 TCP。
- 使用指南 `public/guides/local.html` 的 :982、:1080、:1306：“放行 TCP 端口”改成“放行 TCP 和 UDP 端口”。:1157 讲的是中转入口，第二阶段再改。
- 提供配置下载的地方提示 UDP 需要新版启动器（2.3），用现有的橙色提示样式。

### 4.4 MSBOOST 本地版

安装脚本和执行机代码用 `scripts/syncnode` 同步。本地版还有自己读取 `portBindings` 或 `PublicTCP` 的代码，要单独改：
- `internal/executor/local_deploy.go`、`local_fetch.go`、`local_forward.go`、`local_status.go`；
- `internal/servers/servers.go`、`internal/tasks/tasks.go`；
- 前端 `Servers.tsx`、`Friends.tsx`、`Relays.tsx`。

### 4.5 启动器（MSBOOST-Launcher.exe）

- 设置：`internal/store/store.go:76` 的 `Settings` 增加 `Transport`（`"tcp"` 或 `"udp"`，不写就是 TCP）。`settingsVersion` 不用升。
- 配置：`internal/engine/config.go` 加两个方法：一个列出配置里有哪些传输方式；另一个按传输方式复制一份配置（`proto.Clone`），只保留选中的绑定。磁盘上的配置文件不改。
- 启动：`internal/app/controller.go` 的 `startSession`（:451–475）在调用 `StartEngine` 前选传输方式：设置是 UDP 且配置里有 UDP 才用 UDP，否则用 TCP。筛选后的副本只交给 `StartEngine`，会话里保存的仍是磁盘上的完整配置，另外记下实际使用的方式。
- 预检：用 UDP 启动核心后、接上分流之前（:486 之前），经本地 SOCKS5 向节点的 `127.0.0.1:20424` 发一次 hello，最多等 5 秒。`internal/probe/fullpath.go` 已有 `openFullPathSession` 和 `hello`，导出一个只做 hello 的函数即可。
  - 失败就关掉核心，改用 TCP 重新启动。日志写明原因，并通过 `startSession` 的 `warnings` 提示用户。可能的原因：安全组或本机网络挡了 UDP、运营商限制 UDP。
  - 设置保持 UDP 不变，下次启动再试。
- 网络检测：`Check`（:749–811）空闲时启动的临时核心也按设置选传输方式，结果里写明用的是哪种，方便用户切换后对比。
  - “到中转”（:789）继续用配置里的 TCP 绑定测握手。它用的是完整配置，不受筛选影响。
  - “到游戏服务器”（:802）走隧道，反映实际使用的传输方式。
- 界面：
  - 加速页（`frontend/src/pages/Home.tsx`）配置选择旁加 TCP/UDP 切换，加速中不能改（同 :152 配置选择的做法）。配置里没有 UDP 时不显示，或者置灰；
  - 状态里显示实际使用的方式；
  - 配置页（`frontend/src/pages/Configs.tsx:62`）在入口后面标出支持的方式，由 `internal/store/store.go:310` 的 `describeConfig` 一并返回。
- 游戏的 UDP（语音等）现在就已经经过 mieru：`internal/profiles/profiles.go` 里 MapleStory 是 `ProtocolBoth`。切换的是启动器到节点之间的底层传输，哪些流量被加速不变。
- 测试：绑定筛选、传输方式选择、预检失败退回（用假核心）、旧 settings.json 的读取。
- 更新使用指南和发布说明。

## 5. 第二阶段：中转支持 UDP

两类中转现在都只支持 TCP：
- 受管中转：
  - gost 服务只有 tcp 和 tls（`internal/relayruntime/runtime.go:133`、:162）；
  - 公网端口由 TCP 守卫占着（`socks_guard.go:71`）；
  - 防火墙只有 `-p tcp`（`internal/relayfirewall/firewall.go:35`），也只读 `/proc/net/tcp`（`host_linux.go:86`）；
  - 面板只接受 tcp 和 tls（`internal/control/relay.go:415` 的跳间协议，:701 的可选协议）。
- 自备中转：脚本只放行 `/tcp`（`internal/executor/relay_script.go:32-34`、:144-145）。

要做的事：
1. 每条规则在同一端口再加一个 gost UDP 服务。必须设 `keepalive: true`，并把 `ttl` 调到 30 秒左右：gost 默认一问一答后就关闭通道，而 mieru 的心跳约 5 秒一次。
2. 分配入口端口时，TCP 和 UDP 都要空闲。
3. UDP 端口也要有来源 ACL。TCP 端口的 ACL 现在由守卫负责，UDP 端口没有对应的守卫。
4. 跳与跳之间不能用 TLS：gost 的链式 UDP 会变成 UDP-over-TLS，等于又走回 TCP。建议跨节点直接用 UDP，加来源 IP 白名单。
5. 防火墙维护加 `-p udp`，并读 `/proc/net/udp`、`/proc/net/udp6`。
6. TCP 和 UDP 两个服务的流量合并记账、合并限速。gost 对 UDP 的统计和限速要先实测。
7. 中转验证再用 UDP 跑一遍。
8. 规则支持 UDP 时，`RewriteClientConfig` 保留 UDP 绑定并改写端口（加一个参数）。
9. 中转相关文案，例如 `local.html:1157`。

如果大多数用户走中转，第一阶段的收益有限。第二阶段做不做，看实测数据和走中转的用户比例。

（2026-11-01：决定做，具体设计见第 11 节。）

## 6. 实施顺序

0. 先实测，不写代码（第 7 节）。
1. 数据证明 UDP 有用，就发支持 UDP 的启动器。配置里没有 UDP 绑定时，新启动器和现在完全一样。
2. 改安装脚本、执行机、网站和本地版，然后通知用户升级节点、重新下载配置。
3. 视实测结果和走中转的用户比例，决定是否做第二阶段。

## 7. 第 0 步：手动实测

在一台测试节点上：
1. 在 `/etc/msboost/config.yaml` 的 `listeners` 里加一个监听：

   ```yaml
     - name: msboost-in-udp
       type: mieru
       listen: 0.0.0.0
       port: <与 msboost-in 相同>
       transport: UDP
       traffic-pattern: "IgIQAQ=="
       users:
         "<与 msboost-in 相同>": "<与 msboost-in 相同>"
   ```

   开了低熵的节点，`traffic-pattern` 按第 3 节的表取对应的值。
2. 放行 UDP：`ufw allow <端口>/udp`，云服务商的安全组也要放行。
3. `systemctl restart msboost`，再用 `ss -lun` 确认 UDP 端口在监听。
4. 复制一份 `直连.json`：
   - 把 `"protocol": "TCP"` 改成 `"UDP"`；
   - 在 `mtu` 后面加上 `"trafficPattern": {"nonce": {"applyToAllUDPPacket": true}}`，已有 `trafficPattern` 的话加到里面；
   - 导入现在的启动器。现在的启动器能运行只有 UDP 的配置；网络检测的“到中转”会显示“无 TCP 测试端口”，属于正常。
   - 不要导入同时带 TCP 和 UDP 的配置，现在的启动器会混用。
5. 晚高峰在电信、联通、移动的网络下，用 TCP 配置和 UDP 配置分别测：网络检测的“到游戏服务器”、游戏内延迟和掉线情况、语音。

注意：之后在这台节点上跑安装脚本（包括网站的节点升级）会覆盖手改的 `config.yaml`。

## 8. 验收

节点与网站：
- [ ] 新部署：节点同一端口有 TCP 和 UDP 监听；ufw/firewalld 两条规则都在；`直连.json` 有两个绑定和 nonce；部署结果显示公网 TCP 和公网 UDP。
- [ ] 安全组只放行 TCP：部署成功，公网 UDP 显示不可达。
- [ ] 升级老节点：入口和认证不变，旧配置继续可用，新配置多了 UDP 绑定。
- [ ] UDP 端口被其他程序占用：只开 TCP，有警告，配置里只有 TCP。
- [ ] 安装失败回滚、换端口、卸载清理时，UDP 防火墙规则一并处理，不删别人加的规则。
- [ ] 第一阶段生成的中转配置只有 TCP；用双绑定配置建中转、建前置都能通过。
- [ ] 本地版同上。

启动器：
- [ ] 只有 TCP 的配置：没有 UDP 选项，行为和现在一样。
- [ ] 双绑定配置：选 TCP、选 UDP 都能正常加速；抓包确认只用了选中的那一种。
- [ ] 节点 UDP 不通：5 秒内退回 TCP，有提示，日志写明原因。
- [ ] 网络检测：“到中转”照常显示，“到游戏服务器”反映所选方式。
- [ ] MapleStory 登录、进游戏、语音；长时间挂机不断线。

实测：
- 不做：三家运营商晚高峰 TCP 和 UDP 的对比（10.1）。
- [ ] 低熵各档下 UDP 的吞吐和延迟。

## 9. 风险

| 风险 | 说明 | 应对 |
|---|---|---|
| 运营商限制境外 UDP | 晚高峰限速或丢包，地区和运营商之间差别大 | 默认 TCP；第 0 步实测；预检失败自动退回 |
| 安全组只放行 TCP | 不少云服务商默认不放行 UDP | 部署结果的公网 UDP 检查；指南和安装输出提示；启动器退回 TCP |
| mihomo 同端口两个监听 | 已在 Debian 12/13 真机验证可行（10.4） | — |
| 低熵模式下 UDP 有效载荷变小 | MTU 仍是 1400，低熵改写后每个包能装的数据变少 | 实测吞吐 |
| 旧版启动器混用 | v1.1.2 及更早不筛选绑定 | 先发启动器；网站提示；发布说明 |
| 其他 mieru 客户端 | 不经启动器、直接导入 `直连.json` 时同样会混用 | 不在支持范围 |
| 分流核心的开销 | 分流核心本来就检查所有外发的 TCP 和 UDP 包（`third_party/ProxyBridge/Windows/src/ProxyBridge.c:1220`），改用 UDP 后，启动器自己发往节点的包也要经它放行，和现在的 TCP 包一样 | 实机测 CPU 占用和延迟 |

## 10. 第一阶段实施记录（2026-10-30）

两个仓库的 `feat/udp-transport` 分支都包含这次的版本升级：MSBOOST.DE 的 mihomo v1.19.33，启动器的 mieru v3.39.0。

### 10.1 和计划不同的三处

- **安装脚本用 `MSBOOST_UDP=1` 显式开启 UDP。**
  - 原因：项目约定旧执行机必须能继续领取部署任务（`internal/control/tasks_test.go` 的 `TestLegacyExecutorStillClaimsNonRelayJobs`）。旧执行机解析不了双绑定配置，如果安装脚本默认开 UDP，它会在装完节点后报“配置格式不符合要求”。
  - 新版执行机在部署和升级时都带 `MSBOOST_UDP=1`，所以网站部署的节点默认同时开 TCP 和 UDP，用户不用选。
  - 不带这个变量时，重装沿用 install.env 里记录的 `UDP`；全新安装只开 TCP。
- **控制面新增执行机能力 `node_udp_v1`**（请求头 `X-MSBOOST-Node-UDP`）。只有任务里带的配置有 UDP 绑定时才要求这项能力，例如用 UDP 节点的配置建中转。部署、升级和取回配置不要求。
- **不做晚高峰对比（2026-11-01 决定）。** 第 6 节原定先用第 7 节的实测证明 UDP 有用，再发启动器。现在不做这项对比，发版也不再等这项数据。配置带 UDP 时启动器仍默认 TCP，用户选了 UDP 才用，连不上会自动退回 TCP。

### 10.2 已完成

- **安装脚本**：同端口第二个 mieru 监听；traffic-pattern 一律写 nonce；端口检查和启动后检查都包括 UDP；只在 UDP 端口空闲或由本服务占用时开 UDP；自测增加经 UDP 出站的 hello（`IN-NAME` 分流）；UFW/firewalld 的 UDP 规则单独记录归属，回滚、换端口、改成只开 TCP 时一并处理；install.env 增加 `UDP` 和三项 UDP 规则归属。
- **执行机**：解析“TCP 加同端口 UDP”；中转改写只留 TCP；部署、升级和取回配置结果增加 `publicUDP`（经 UDP 隧道向 `127.0.0.1:20424` 发 hello）；清理时删除 UDP 规则。
- **网站**：检查结果加“公网 UDP 可达性”一行，没有这一项的旧结果不显示；配置带 UDP 时，下载处提示需要最新版启动器；上传配置接受双绑定。
- **启动器**：
  - 设置 `transport`；
  - 加速页“节点配置”一行右侧的 TCP/UDP 切换，只在当前配置带 UDP 时显示；
  - 启动时只保留选中的绑定；UDP 先发 hello，最多 5 秒，失败就改用 TCP 并提示；
  - 网络检测按所选方式运行，并写明用的是哪种；
  - 配置页标出“支持 UDP”；
  - `msboostctl transport <tcp|udp>`。
- **测试**：两个仓库的单元测试全部通过，MSBOOST.DE 的 HTTP 和浏览器测试也全部通过。新增的测试覆盖：生成的配置、UDP 开关的判断、防火墙规则归属和回滚、配置解析、公网 UDP 检查、能力要求、绑定筛选、启动退回 TCP 和网络检测。

### 10.3 还没做或没验证

- **本地版**：要等 MSBOOST.DE 发出包含这些改动的标签，再用 `syncnode` 同步。另外本地版自己的部署代码也要带 `MSBOOST_UDP=1`，并改 `local_*.go`、前端页面和使用指南 `local.html` 里的“放行 TCP 端口”。
- **实机**：节点、执行机和启动器（网络检测、开始加速、UDP 不通时退回 TCP）已真机测试（10.4）。晚高峰三家运营商的 TCP/UDP 对比不做（10.1）。
- **执行机需要升级**：旧执行机部署出来的仍是只有 TCP 的节点，公网 UDP 也不会显示。
- **发版时补上**：发布说明，以及启动器使用指南里的连接方式说明。

### 10.4 真机测试（2026-11-01）

五台临时 VPS：一台当执行机，运行一个临时小程序，直接调用执行机的部署、升级、取回配置、清理和公网 UDP 检查代码，用密钥登录并核对主机指纹；另外四台当节点，系统为 Debian 12/13。启动器的网络检测在开发者的 Windows 电脑上用 `msboostctl` 运行，开始加速在同一台电脑的启动器界面里操作，节点上用 nft 计数和丢包。全部通过：

| 测试 | 结果 |
|---|---|
| 全新部署（Debian 12，开着 ufw） | 同一端口 TCP 和 UDP 都由 msboost 监听；ufw 加了 tcp/udp 两条规则，`install.env` 记为归我们管；客户端配置双绑定，并带低熵和 nonce；UDP 自测通过；公网 TCP、UDP 都可达 |
| 从 v1.19.32 升级（低熵 48） | 端口、认证、低熵档位不变；开启 UDP；结果提示“节点已升级并开启了 UDP” |
| 从 v1.19.30 升级（机器上有 6 条 gost 中转） | 同上；没有记录低熵档位的旧安装保持关闭；6 条中转照常运行 |
| 取回配置 | 结果带公网 UDP 可达 |
| 节点 UDP 被丢弃（iptables/nft） | 公网 UDP 检查约 9 秒后报不可达，恢复后 1.5 秒报可达；启动器网络检测给出 UDP 被拦的提示（见下） |
| 重装不带 `MSBOOST_UDP` | 沿用记录，保持 UDP |
| `MSBOOST_UDP=0` | 只开 TCP，配置只剩 TCP 绑定 |
| 换到 UDP 被其他程序（mita）占用的端口 | 警告后只开 TCP；旧端口归我们管的 tcp/udp 规则删除；换回后恢复 TCP+UDP，旧规则清掉 |
| 模拟旧执行机全新安装（不带 `MSBOOST_UDP`） | 只开 TCP |
| 回环 UDP 被丢弃时开 UDP | UDP 自测失败 3 次，事务回滚；三份配置文件指纹与回滚前一致，服务照常运行 |
| 清理（开着 ufw） | 预览列出 `ufw::<端口>/tcp` 和 `ufw::<端口>/udp`；清理后规则和文件全部删除，其他程序不受影响 |
| 启动器网络检测（凌晨，非高峰） | 三台节点 TCP 和 UDP 都可用，延迟基本相同（例如 185–194 ms 对 185–189 ms）；升级前的旧配置（只有 TCP、没有 nonce）照常可用；旧配置选 UDP 时自动走 TCP |
| 启动器开始加速（CI 构建版 dev-c6f9386，以管理员身份运行） | 选 UDP 开始加速，节点侧计数确认流量是 UDP |
| UDP 被挡时开始加速 | 约 5 秒后提示“UDP 连不上节点，本次改用 TCP 加速”；节点侧 TCP 包增加，UDP 包全被丢弃 |
| UDP 恢复后再开始 | 不再退回，状态显示“经 UDP 连接节点”，节点侧计数确认走 UDP |

测试中发现并已修复：网络检测走 UDP 被拦时，原先的失败原因写成“节点未提供兼容的版本 2 全程 TCP 测速服务”。现在，如果出口 IP 和到游戏服务器都失败、而同一端口的 TCP 握手正常，就提示“经 UDP 连不上节点，可能是节点没有放行 UDP，或本机网络限制了 UDP；可改用 TCP”。

已知限制：UDP 只在开始加速时检查一次。加速过程中 UDP 断了不会自动切换，网络检测会给出上面的提示，用户要停止后重新开始，这时会退回 TCP。要不要做“加速中自动切换”，等有用户反馈再定。

## 11. 第二阶段设计（2026-11-01）

决定先做第二阶段，与第一阶段一起发版。代码基准是两个仓库的 `feat/udp-transport`。

### 11.1 gost UDP 实测

用现在中转用的 gost v3.3.0。一台测试机在同一端口开 TCP 和 UDP 两个转发服务，目标是开着 UDP 的节点；另一台用 mihomo v1.19.33 当客户端，经中转下载测速：

| 测试 | 结果 |
|---|---|
| UDP 转发，listener 设 `keepalive: true`、`ttl: 30s` | 经中转的 mieru UDP 正常；空闲 45 秒（超过 ttl）后再用，立即恢复 |
| 不设 keepalive | mieru UDP 完全不通：gost 收到一次回复就关闭通道 |
| 流量统计 | observer 照常上报 UDP 服务的累计上下行字节；`currentConns` 恒为 0 |
| 限速效率（只跑一种方式，30 秒） | 限额 5 Mbps 时 UDP 跑到 26%、TCP 70%；限额 20 Mbps 时 UDP 41%、TCP 75%。UDP 不会超限，但跑不满 |
| TCP 和 UDP 共用一个限速器，同时跑 | UDP 被压到几乎为零：限额 2 Mbps 时 1.8 KB/s，10 Mbps 时 38 KB/s |
| 各用一个限速器，同时跑 | 两者各有额度 |
| UDP 端口绑定失败 | gost 整个进程退出，同一进程里的 TCP 服务也跟着停 |

由此定下：
- UDP 服务必须开 keepalive，ttl 用 30 秒。
- TCP 和 UDP 各用一个限速器，额度都等于规则速率，不合并限速：合并会让两种方式同时使用时 UDP 不可用。启动器一次只用一种方式，所以平常不会超额；同一用户在两台电脑上分别用 TCP 和 UDP 时，最多到两倍速率。流量计费照样合并（11.3 第 5 条）。
- 接近限额时 UDP 比 TCP 慢。游戏本身的流量远低于限额，影响不大，发布说明里写明。
- UDP 出问题不能拖垮 TCP：gost 只在 Agent 预留的回环端口上收 UDP，公网 UDP 端口由 Agent 自己绑定，绑不上只影响 UDP。

### 11.2 滥用风险

UDP 没有握手，来源地址可以伪造。中转如果把 UDP 转给 DNS、NTP 这类会回应的服务，就成了反射放大的跳板；TCP 中转不会。所以：
- 目标端口小于 1024 时不开 UDP，挡住常见的反射端口。节点默认用 20000–59999 的随机端口；用户自己指定 1024 以下端口的节点，走中转时只有 TCP。
- 受管中转：新规则先只开 TCP。中转校验证明目标是 MSBOOST 节点后，再给各段开 UDP，这时该规则的 gost 会重启一次。校验为“强制”时，配置要通过校验才能下载，重启时用户还没开始用；为“只记录”时，可能断开一次刚建立的连接。校验关闭的站点不开 UDP。
- 自备中转和前置机装在用户或中转码提供者的服务器上：源配置带 UDP 就开。中转码的校验在安装后立即进行，不通过就整个删掉，现在就是这样。

### 11.3 受管中转（Agent）

1. **规则**：`relayruntime.Rule` 加 `UDP bool`（`json:"udp,omitempty"`）。`RuntimeHash` 只在 UDP 为 true 时把它算进去，已有规则的哈希不变，不会被重新下发。
2. **能力**：Agent 上报新能力 `udp_forward_v1`。控制面只给全线 Agent 都报了这项能力的规则开 UDP，旧 Agent 不会收到带 `udp` 的规则。
3. **gost**：同一进程加第二个服务 `<规则 ID>/udp`：handler 和 listener 都是 udp，listener 开 keepalive、ttl 30 秒；监听 Agent 预留的回环 UDP 端口；目标与 TCP 相同（下一跳或节点的同一端口）；跳与跳之间一律明文 UDP，不用 TLS；单独一个限速器，额度同规则速率；observer 与 TCP 共用。
4. **UDP 守卫**（新文件 `udp_guard.go`）：
   - Agent 绑定公网 UDP 端口，只放行 `AllowedSources`（与 TCP 相同；入口为空，即全部放行）；
   - 每个客户端地址开一个连到 gost 回环端口的 socket，空闲 90 秒回收，最多 4096 个；
   - 不做 SOCKS 屏蔽，那是针对明文 TCP 的；
   - 公网 UDP 端口绑不上时，只记日志并定期重试，不影响 TCP，也不改变规则的 ACK 状态。
5. **计费**：observer 上报的 TCP、UDP 两个服务的累计字节相加，再交给 `recordV2TrafficLocked`。两者各自单调递增，和也单调。
6. **防火墙**（`internal/relayfirewall`）：
   - 端口来源除了 MainPID 的 TCP 监听，再读 `/proc/<pid>/net/udp`、`udp6` 里通配地址的 socket；
   - 链里加 `-p udp` 的 ACCEPT 规则，规则形状照样严格校验；状态文件增加 `udpPorts`；
   - 规则的就绪状态仍只看 TCP。
   - 旧版 helper 看到链里的 udp 规则会认为链不归自己管并停止维护（fail closed），所以不支持降级。
7. **控制面**：
   - 线路的全部 Agent 都支持 UDP，源配置带 UDP，目标端口不小于 1024，校验没关闭：新规则记为 `udp: "pending"`，各段先不开 UDP。
   - 校验通过（`applyRuleVerify`）时，`pending` 改为 `on`，各段 `Runtime.UDP = true`，下一次同步按新的运行哈希重新下发；下载的配置同时加上 UDP 绑定（端口与 TCP 相同）。
   - 已有规则不自动开 UDP：开 UDP 要重启 gost，会断开正在用的连接。想用 UDP 的用户删掉重建。
   - 带前置的规则：前置机也要开了 UDP（11.4）才算支持；前置机地址照样是入口唯一允许的来源。
   - `RewriteClientConfig` 仍只留 TCP；另加 `WithUDPBinding`，在入口转发 UDP 时补上同端口的 UDP 绑定。
   - 校验：配置带 UDP 时，执行机再经入口跑一次 UDP 检查（`ProbeUDP`），结果只做提示，和第一阶段的公网 UDP 检查一样。
8. **网站**：规则卡片标出 UDP 状态（校验通过后开启、已开启），已开启时提示需要新版启动器、旧配置要重新下载；隧道列表标出整条线路是否支持 UDP。

### 11.4 自备中转和前置机（执行机部署）

- gost 配置加 UDP 服务，直接监听公网 UDP 端口。Python 守卫只为 TCP 屏蔽明文 SOCKS；入口本来就对公网开放，不需要来源限制。
- 选端口时 UDP 也要空闲；ufw/firewalld 放行 `/udp` 并单独记录归属，失败回滚和清理时一并删除。
- 安装后确认 UDP 端口由本服务（unit 里的进程）绑定。
- 生成的配置保留 UDP 绑定；有前置机时前置机也开 UDP。执行机结果里写明开没开 UDP，旧执行机不写，按没开处理。
- 中转管理的列表里显示是否开了 UDP。

### 11.5 本地版

本地版用 `syncnode` 同步后自然带上。中转相关的文案（`local.html:1157` 的“放行 TCP 高位端口”）一并改。

### 11.6 测试

- 单元测试：UDP 为 false 时运行哈希与旧版相同；gost 配置；UDP 守卫的来源过滤、会话回收和上限；observer 计数合并；防火墙规则解析；控制面开 UDP 的条件和校验通过后开启；`RewriteClientConfig`；自备中转脚本。
- Linux 集成测试：已有真实 gost 的集成测试（`internal/relayruntime/gost_integration_test.go`）加 UDP。
- 真机：受管中转单跳和两跳，启动器选 UDP；自备中转和前置机；限速和计费。

### 11.7 实施记录

代码在 MSBOOST.DE 的 `feat/relay-udp` 分支，基于 `feat/udp-transport`。按 11.3、11.4 实现，另外有几处：

- **自备中转的管理操作**：中转列表原来只认一个服务的配置，开了 UDP 的中转会被跳过；改速率时只重写一个限速器；删除时只删 TCP 规则。三处都改了。Python 守卫启动时也只认一个服务，一并改了。
- **下载的配置**：UDP 开启前不带 UDP 绑定，开启时再补上，所以之前下载的配置要重新下载才有 UDP。网站在规则卡片上提示。
- **自备中转的“公网 UDP 可达性”**：直接用校验里经入口做的 UDP 检查，不再另查一次。
- **就绪判断**：端到端测试发现，开 UDP 后、Agent 应用新规则之前，控制面已经把这一段算作就绪。现在和速率一样，记下下发时的 UDP（`IssuedUDP`），ACK 之前不算就绪；备份门禁和恢复核对也一起比较。
- **兼容**：
  - Agent 解析同步响应时拒绝未知字段，所以旧 Agent 绝不能收到带 `udp` 的规则，靠 `udp_forward_v1` 能力保证。
  - 网站解析执行机结果时同样拒绝未知字段。网站先于执行机升级，所以结果里的新字段（`hop.udp`、`verify.udp`）没有问题。
- **测试**：单元测试、Linux 上用真实 gost 的集成测试（两跳 UDP 加 TLS 跳；Python 守卫加 UDP）、HTTP 测试和 99 项浏览器测试全部通过。

### 11.8 真机测试（2026-11-01）

用 10.4 的测试机：一台当执行机，跑一个临时程序，直接调用执行机的中转代码和 Agent 的规则代码；中转机为 Debian 13，测试时装上 ufw，测完卸载；目标是开着 UDP 的节点。

| 测试 | 结果 |
|---|---|
| 执行机安装自备中转 | 结果为“TCP 和 UDP 中转服务已真实绑定端口”；公网 TCP、UDP 都可达；校验通过，经中转的 UDP 也可用。TCP 由 Python 守卫监听，UDP 由 gost 监听；ufw 加了 tcp、udp 两条规则，归属分开记录 |
| 经自备中转下载（限额 10 Mbps） | TCP 0.91 MB/s，UDP 0.45 MB/s，出口都是节点 |
| 改速率为 5 Mbps | 两个限速器都改为 625000 B/s；服务重启后 UDP 照常 |
| 读取中转列表 | 列出这条中转，并标出 UDP |
| 删除 | 配置目录、unit 和 ufw 的 tcp、udp 规则全部删除 |
| 强制校验不通过（目标不是 mieru） | 执行机删除刚装的中转，ufw 只剩原有规则 |
| Agent 规则在公网端口转发到 UDP 节点（限额 20 Mbps） | 公网 TCP、UDP 端口都由 Agent 占用；TCP 1.86 MB/s，UDP 1.13 MB/s；计费为两者之和；一个客户端一个 UDP 会话 |
| Agent 来源限制 | 不在名单里的地址，TCP 和 UDP 都被丢弃 |
| 控制面、正式 Agent 和真实 gost 一起跑（隔离网络命名空间，CI 也跑） | 校验前只走 TCP，UDP 不通；校验通过后重新下发，同端口 UDP 可用，TCP 照常；UDP 字节计入规则流量 |

两跳加前置机的整条受管线路也在真机上走了一遍。控制面是执行机上的临时程序：只监听本机地址，经 SSH 反向隧道和自签名证书提供 HTTPS；前置机部署和中转校验都调用真实的执行机代码。两台 Agent 用正式的 `deploy/install-agent.sh` 安装（含防火墙维护服务），测完用 `uninstall-agent.sh` 卸载。路径为：客户端 → 前置机 → 入口 Agent → 出口 Agent（两跳之间 TCP 走 TLS）→ 开着 UDP 的节点。

| 测试 | 结果 |
|---|---|
| 新建规则 | 两台 Agent 都上报 UDP 能力；规则记为等待开 UDP；入口只放行前置机地址，出口只放行入口地址 |
| 部署前置机 | 执行机经 SSH 部署，前置机同时转发 UDP |
| 第一次校验（经前置机和两跳） | 通过；随后规则开 UDP，两台 Agent 5 秒内应用 |
| 第二次校验 | 经整条链路走 UDP，可达 |
| 客户端用下载的配置 | 配置为前置机端口的 TCP 加 UDP；两种方式出口都是节点（经四段，TCP 0.43 MB/s，UDP 0.20 MB/s） |
| Agent 端口和防火墙维护 | 规则端口的 TCP、UDP 都由 Agent 进程占用；防火墙维护服务同时报出这个 TCP 和 UDP 端口 |
| 计费 | 规则记到 TCP 加 UDP 的流量 |
| 删除规则 | 两跳都确认停止，端口释放；前置机删除干净 |

测试中发现：出口机上原有一个执行机服务，与 Agent 共用 `/usr/local/bin/msboost-agent`。安装中转 Agent 会连带替换这个程序，这是安装脚本原有的行为。那个执行机早已因令牌被拒而不停重启，与本次无关；测完已换回原程序。
