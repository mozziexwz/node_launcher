//go:build windows

// Command msboostctl drives the launcher core from the command line, through
// the same controller the UI uses. It is a development and acceptance tool
// (M1) and is not shipped to users.
//
// Data lives beside the executable (configs\, logs\, settings.json), exactly
// as in the portable release; -data overrides the folder.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mozziexwz/node_launcher/internal/app"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/profiles"
	"github.com/mozziexwz/node_launcher/internal/store"
	"github.com/mozziexwz/node_launcher/internal/winsys"
)

const instanceName = "MSBOOST-Launcher-Core"

func usage() {
	fmt.Print(`msboostctl：MSBOOST 启动器命令行（开发与验收用）

用法：msboostctl <命令> [参数]

  status                       权限、冲突与当前设置
  configs                      列出 configs\ 中的节点配置（* 为当前配置）
  import <文件> [-overwrite]   导入节点配置到 configs\
  import-legacy <旧版文件夹>   从旧 BAT 启动器文件夹导入配置
  use <配置文件名>             选择节点配置
  target <IP:端口>             设置全程测速目标
  addon <discord|steam> <on|off>  可选加速开关
  transport <tcp|udp>          连接方式；配置里没有 UDP 时总是 TCP
  check                        网络检测：到游戏服务器 IPv4、到中转、到游戏
  run [-duration 30m]          开始加速，Ctrl+C 停止（需要管理员）
  clean                        清理驱动、防火墙规则与运行文件（需要管理员）

通用参数（放在命令之后）：
  -data 文件夹   数据目录，默认是本程序所在文件夹
  -core 路径     ProxyBridgeCore.dll，默认是本程序所在文件夹中的同名文件
`)
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "help" {
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "[失败]", err)
		os.Exit(1)
	}
}

type env struct {
	layout store.Layout
	log    *logx.Logger
	ctl    *app.Controller
	stop   func()
}

func setup(fs *flag.FlagSet, args []string) (*env, []string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	dataDir := fs.String("data", filepath.Dir(exe), "数据目录")
	corePath := fs.String("core", filepath.Join(filepath.Dir(exe), "ProxyBridgeCore.dll"), "ProxyBridgeCore.dll 路径")
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	layout := store.NewLayout(*dataDir)
	if err := layout.Ensure(); err != nil {
		return nil, nil, err
	}
	log, err := logx.New(layout.Logs, 7)
	if err != nil {
		return nil, nil, err
	}
	lines, cancel := log.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for line := range lines {
			fmt.Printf("%s [%s] %s\n", line.Time.Format("15:04:05"), line.Tag, line.Text)
		}
	}()
	// The CLI is a development tool: it loads the runtime files from -core.
	e := &env{layout: layout, log: log, ctl: app.NewWindows(layout, log, *corePath, nil)}
	e.stop = func() {
		cancel()
		<-done
		log.Close()
	}
	return e, fs.Args(), nil
}

func run(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	switch cmd {
	case "status", "configs", "import", "import-legacy", "use", "target", "addon", "transport", "check", "run", "clean":
	default:
		usage()
		return fmt.Errorf("未知命令 %q", cmd)
	}
	overwrite := fs.Bool("overwrite", false, "导入时覆盖同名但内容不同的配置")
	duration := fs.Duration("duration", 0, "run：运行时长，0 表示等待 Ctrl+C")
	e, rest, err := setup(fs, reorder(args))
	if err != nil {
		return err
	}
	defer e.stop()

	switch cmd {
	case "status":
		return cmdStatus(e)
	case "configs":
		return cmdConfigs(e)
	case "import":
		if len(rest) != 1 {
			return errors.New("用法：msboostctl import <文件> [-overwrite]")
		}
		entry, err := e.layout.ImportConfig(rest[0], *overwrite)
		if err != nil {
			return err
		}
		fmt.Printf("已导入 %s（方案 %s，入口 %s）\n", entry.Name, entry.Profile, entry.Entrance)
		return nil
	case "import-legacy":
		if len(rest) != 1 {
			return errors.New("用法：msboostctl import-legacy <旧版文件夹>")
		}
		return cmdImportLegacy(e, rest[0])
	case "use":
		if len(rest) != 1 {
			return errors.New("用法：msboostctl use <配置文件名>")
		}
		return e.ctl.SelectConfig(rest[0])
	case "target":
		if len(rest) != 1 {
			return errors.New("用法：msboostctl target <IP:端口>")
		}
		host, portText, err := net.SplitHostPort(rest[0])
		if err != nil {
			return fmt.Errorf("格式应为 IP:端口：%w", err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return fmt.Errorf("端口无效：%q", portText)
		}
		if err := e.ctl.SetProbeTarget(host, port); err != nil {
			return err
		}
		fmt.Printf("全程测速目标：%s\n", rest[0])
		return nil
	case "addon":
		if len(rest) != 2 || (rest[1] != "on" && rest[1] != "off") {
			return errors.New("用法：msboostctl addon <discord|steam> <on|off>")
		}
		if err := e.ctl.SetAddon(rest[0], rest[1] == "on"); err != nil {
			return err
		}
		fmt.Printf("%s：%s\n", rest[0], rest[1])
		return nil
	case "transport":
		if len(rest) != 1 || (rest[0] != "tcp" && rest[0] != "udp") {
			return errors.New("用法：msboostctl transport <tcp|udp>")
		}
		if err := e.ctl.SetTransport(rest[0]); err != nil {
			return err
		}
		fmt.Printf("连接方式：%s\n", strings.ToUpper(rest[0]))
		return nil
	case "check":
		return cmdCheck(e)
	case "run":
		return cmdRun(e, *duration)
	case "clean":
		return cmdClean(e)
	}
	return nil
}

// reorder lets flags follow positional arguments ("import x.json -overwrite").
func reorder(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && (a == "-data" || a == "-core" || a == "-duration") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func cmdStatus(e *env) error {
	elevated, conflicts := e.ctl.Preflight()
	s := e.ctl.Settings()
	fmt.Printf("管理员权限：%v\n", elevated)
	fmt.Printf("数据目录：%s\n", e.layout.Root)
	fmt.Printf("当前配置：%s\n", orNone(s.CurrentConfig))
	fmt.Printf("全程测速目标：%s:%d\n", s.ProbeTarget.Address, s.ProbeTarget.Port)
	transport := "TCP"
	if s.Transport == "udp" {
		transport = "UDP（配置里有 UDP 时）"
	}
	fmt.Printf("连接方式：%s\n", transport)
	for _, a := range profiles.Addons() {
		fmt.Printf("可选加速 %s：%v\n", a.Name, s.Addons[a.ID])
	}
	if len(conflicts) == 0 {
		fmt.Println("冲突检测：无")
	}
	for _, c := range conflicts {
		level := "提示"
		if c.Blocking {
			level = "阻止"
		}
		fmt.Printf("冲突检测（%s）：%s\n", level, c.Message)
	}
	return nil
}

func cmdConfigs(e *env) error {
	list, err := e.layout.ListConfigs()
	if err != nil {
		return err
	}
	current := e.ctl.Settings().CurrentConfig
	if len(list) == 0 {
		fmt.Printf("%s 中没有配置。用 msboostctl import <文件> 导入。\n", e.layout.Configs)
	}
	for _, c := range list {
		mark := " "
		if c.Name == current {
			mark = "*"
		}
		if c.Err != nil {
			fmt.Printf("%s %s　无效：%v\n", mark, c.Name, c.Err)
			continue
		}
		fmt.Printf("%s %s　方案 %s　入口 %s\n", mark, c.Name, c.Profile, c.Entrance)
	}
	return nil
}

func cmdImportLegacy(e *env, dir string) error {
	result, err := e.layout.ImportLegacy(dir)
	if err != nil {
		return err
	}
	for _, c := range result.Imported {
		fmt.Printf("已导入 %s\n", c.Name)
	}
	for _, c := range result.Skipped {
		fmt.Printf("已跳过 %s：%v\n", c.Name, c.Err)
	}
	if result.Current != "" {
		if err := e.ctl.SelectConfig(result.Current); err != nil {
			return err
		}
		fmt.Printf("已选择旧版最后加载的配置：%s\n", result.Current)
	}
	return nil
}

func cmdCheck(e *env) error {
	release, err := winsys.AcquireInstance(instanceName)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, err := e.ctl.Check(ctx)
	if err != nil {
		return err
	}
	if r.ExitIPError != "" {
		fmt.Printf("当前到游戏服务器 IPv4：查询失败；%s\n", r.ExitIPError)
	} else {
		fmt.Printf("当前到游戏服务器 IPv4：%s\n", r.ExitIP)
	}
	fmt.Printf("当前到中转服务器延迟：%s　%s\n", r.Entrance.Compact(), detail(r.EntranceTarget, r.Entrance.Detail()))
	fmt.Printf("当前到游戏服务器延迟（%s）：%s　%s\n", strings.ToUpper(r.Transport), r.Game.Compact(), detail(r.GameTarget, r.Game.Detail()))
	return nil
}

func cmdRun(e *env, duration time.Duration) error {
	release, err := winsys.AcquireInstance(instanceName)
	if err != nil {
		return err
	}
	defer release()
	warnings, err := e.ctl.Start()
	for _, w := range warnings {
		fmt.Println("[注意]", w)
	}
	if err != nil {
		return err
	}
	defer e.ctl.Shutdown()
	fmt.Println("加速已开始。现在可以打开 MapleStory。按 Ctrl+C 停止。")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	var timeout <-chan time.Time
	if duration > 0 {
		timeout = time.After(duration)
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			st := e.ctl.Status()
			fmt.Printf("%s [状态] 已运行 %v｜已使用流量 %s｜当前速率 ↑ %s/s ↓ %s/s｜经加速的连接：累计 %d，当前 %d\n",
				time.Now().Format("15:04:05"), time.Since(st.Since).Round(time.Second),
				app.FormatBytes(st.Upload+st.Download), app.FormatBytes(st.UploadRate), app.FormatBytes(st.DownloadRate),
				st.Accepted, st.Active)
		case <-stop:
			fmt.Println("正在停止……")
			return e.ctl.Stop()
		case <-timeout:
			fmt.Println("已到设定时长，正在停止……")
			return e.ctl.Stop()
		}
	}
}

func cmdClean(e *env) error {
	release, err := winsys.AcquireInstance(instanceName)
	if err != nil {
		return err
	}
	defer release()
	if !winsys.IsElevated() {
		return errors.New("清理需要管理员权限")
	}
	exe, _ := os.Executable()
	report, err := e.ctl.Cleanup(exe)
	if err != nil {
		return err
	}
	if len(report) == 0 {
		fmt.Println("没有需要清理的内容")
	}
	return nil
}

func detail(target, text string) string {
	if target == "" {
		return ""
	}
	return "［" + target + "，" + text + "］"
}

func orNone(s string) string {
	if s == "" {
		return "（未选择）"
	}
	return s
}
