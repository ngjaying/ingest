// Command watch: 轮询可移动卷，新卷出现即起子进程跑无头 ingest。
//
// 设计说明：watcher 只做 supervisor——发现卷、匹配设备规则、按设备专属
// target（没有则用全局 --target）起 `ingest --source --target --yes` 子进程，
// 完事 toast。拷贝/校验/历史全在子进程里，崩一个不影响 watcher。
// 点按发现 = 直接运行一次 `ingest`（交互式），watch 负责常驻自动。
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hktkzyx/ingest/internal/config"
	"github.com/hktkzyx/ingest/internal/device"
	"github.com/hktkzyx/ingest/internal/mount"
	"github.com/hktkzyx/ingest/internal/notify"
)

type watchOpts struct {
	interval int
	target   string
	dryRun   bool
	rawDir   string
	videoDir string
	tpl      string
}

func watchCmd() *cobra.Command {
	var o watchOpts
	c := &cobra.Command{
		Use:   "watch",
		Short: "常驻监视可移动卷，自动无头导入",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWatch(cmd.Context(), o)
		},
	}
	c.Flags().IntVar(&o.interval, "interval", 3, "轮询可移动卷的秒数")
	c.Flags().StringVarP(&o.target, "target", "t", defaultTarget(), "无设备专属 target 时的全局目标根目录")
	c.Flags().BoolVar(&o.dryRun, "dry-run", false, "透传给子进程，只预览不拷贝")
	c.Flags().StringVar(&o.rawDir, "raw-dir", "", "透传给子进程，RAW 进子目录")
	c.Flags().StringVar(&o.videoDir, "video-dir", "", "透传给子进程，视频进子目录")
	c.Flags().StringVar(&o.tpl, "template", "", "透传给子进程，路径模板（空则用默认）")
	return c
}

// classifyVolumes 纯函数：对比已知集与当前集，返回新增和消失的卷路径。
func classifyVolumes(known map[string]bool, current []mount.Volume) (added []mount.Volume, gone []string) {
	seen := make(map[string]bool, len(current))
	for _, v := range current {
		seen[v.Path] = true
		if !known[v.Path] {
			added = append(added, v)
		}
	}
	for p := range known {
		if !seen[p] {
			gone = append(gone, p)
		}
	}
	return added, gone
}

func runWatch(ctx context.Context, o watchOpts) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	devicesPath, err := expandPath(flagDevicesPath)
	if err != nil {
		return fmt.Errorf("--devices: %w", err)
	}
	rules, err := device.LoadOrInit(devicesPath)
	if err != nil {
		return fmt.Errorf("load devices: %w", err)
	}
	settings, err := config.LoadOrInit(config.DefaultConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("self path: %w", err)
	}
	fmt.Printf("watch: 每 %d 秒轮询可移动卷 (Ctrl+C 退出)\n", o.interval)
	known := make(map[string]bool)
	ignored := make(map[string]bool)
	tick := time.NewTicker(time.Duration(o.interval) * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "watch: 退出")
			return nil
		case <-tick.C:
			vols, err := mount.List()
			if err != nil {
				fmt.Fprintf(os.Stderr, "watch: 枚举卷失败: %v\n", err)
				continue
			}
			added, gone := classifyVolumes(known, vols)
			for _, g := range gone {
				delete(known, g)
				delete(ignored, g)
			}
			for _, v := range added {
				known[v.Path] = true
				m := device.Detect(rules, v.Path, v.Label)
				if m == nil {
					if !ignored[v.Path] {
						ignored[v.Path] = true
						msg := fmt.Sprintf("未知设备 %s(%s)，已忽略", v.Path, v.Label)
						fmt.Fprintln(os.Stderr, "watch: "+msg)
						notify.Toast("ingest", msg)
					}
					continue
				}
				target := m.Rule.Target
				if target == "" {
					target = o.target
				}
				if settings.StagingDir != "" {
					runStaged(ctx, exe, o, m.Rule.Name, v.Path, settings, target)
					continue
				}
				args := childArgs(o, v.Path, target)
				fmt.Printf("watch: 发现 %s(%s) → %s\n", m.Rule.Name, v.Path, target)
				notify.Toast("ingest", fmt.Sprintf("发现 %s，开始导入", m.Rule.Name))
				if err := runChild(ctx, exe, args); err != nil {
					msg := fmt.Sprintf("%s 导入失败: %v", m.Rule.Name, err)
					fmt.Fprintln(os.Stderr, "watch: "+msg)
					notify.Toast("ingest", msg)
					continue
				}
				notify.Toast("ingest", fmt.Sprintf("%s 导入完成 → %s", m.Rule.Name, filepath.Base(target)))
			}
		}
	}
}

// childArgs 组装子进程参数（两段编排与直拷共用）。
func childArgs(o watchOpts, source, target string) []string {
	args := []string{"--source", source, "--target", target, "--yes"}
	if o.dryRun {
		args = append(args, "--dry-run")
	}
	if o.rawDir != "" {
		args = append(args, "--raw-dir", o.rawDir)
	}
	if o.videoDir != "" {
		args = append(args, "--video-dir", o.videoDir)
	}
	if o.tpl != "" {
		args = append(args, "--template", o.tpl)
	}
	return args
}

func runChild(ctx context.Context, exe string, args []string) error {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runStaged 两段式：卡 → staging（hook 在此跑 rate）→ NAS 目标；成功后按配置清 staging。
func runStaged(ctx context.Context, exe string, o watchOpts, devName, volPath string, settings config.Settings, finalTarget string) {
	stageRoot := filepath.Join(settings.StagingDir, fmt.Sprintf("%s-%s", sanitizeLabel(volPath), time.Now().Format("20060102T150405")))
	fmt.Printf("watch: %s 两段式：%s → staging %s\n", devName, volPath, stageRoot)
	notify.Toast("ingest", fmt.Sprintf("发现 %s，staging 导入", devName))
	if err := runChild(ctx, exe, childArgs(o, volPath, stageRoot)); err != nil {
		msg := fmt.Sprintf("%s staging 失败: %v", devName, err)
		fmt.Fprintln(os.Stderr, "watch: "+msg)
		notify.Toast("ingest", msg)
		return
	}
	if dirIsEmpty(stageRoot) {
		fmt.Printf("watch: staging 为空（全是已入库），跳过转拷\n")
		notify.Toast("ingest", fmt.Sprintf("%s 无新增，跳过", devName))
		return
	}
	fmt.Printf("watch: 转拷 %s → %s\n", stageRoot, finalTarget)
	if err := runChild(ctx, exe, childArgs(o, stageRoot, finalTarget)); err != nil {
		msg := fmt.Sprintf("%s 转拷 NAS 失败: %v（staging 保留，可重跑）", devName, err)
		fmt.Fprintln(os.Stderr, "watch: "+msg)
		notify.Toast("ingest", msg)
		return
	}
	if settings.CleanupStaging && !o.dryRun {
		if err := os.RemoveAll(stageRoot); err != nil {
			fmt.Fprintf(os.Stderr, "watch: 清 staging 失败 %s: %v\n", stageRoot, err)
		}
	}
	notify.Toast("ingest", fmt.Sprintf("%s 入库完成 → %s", devName, filepath.Base(finalTarget)))
}

// sanitizeLabel 把卷路径变成目录名安全串（E:\ → E）。
func sanitizeLabel(volPath string) string {
	s := strings.TrimRight(volPath, `\/ `)
	s = strings.ReplaceAll(s, ":", "")
	if s == "" {
		s = "vol"
	}
	return s
}

// dirIsEmpty 目录不存在或无文件即视为空。
func dirIsEmpty(dir string) bool {
	empty := true
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			empty = false
		}
		return nil
	})
	return empty
}
