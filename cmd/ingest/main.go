package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hktkzyx/ingest/internal/config"
	"github.com/hktkzyx/ingest/internal/copier"
	"github.com/hktkzyx/ingest/internal/db"
	"github.com/hktkzyx/ingest/internal/device"
	"github.com/hktkzyx/ingest/internal/mount"
	"github.com/hktkzyx/ingest/internal/period"
	"github.com/hktkzyx/ingest/internal/prompt"
	"github.com/hktkzyx/ingest/internal/scanner"
	"github.com/hktkzyx/ingest/internal/template"
)

var version = "0.1.1"

const defaultTemplate = "{date_start}[_{date_end}]-{event_name}/origin-{device_name}"

var (
	flagSource      string
	flagTarget      string
	flagName        string
	flagDevice      string
	flagFrom        string
	flagTo          string
	flagTemplate    string
	flagDBPath      string
	flagDevicesPath string
	flagConfigPath  string
	flagGapDays     int
	flagDryRun      bool
	flagYes         bool
	flagVerbose     bool
	flagOverwrite   bool
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Smart media ingestion tool",
		RunE:  runIngest,
	}
	cmd.PersistentFlags().StringVar(&flagDevicesPath, "devices", device.DefaultConfigPath(),
		"path to devices.yaml (auto-created with built-in defaults if missing)")
	cmd.PersistentFlags().StringVar(&flagConfigPath, "config", config.DefaultConfigPath(),
		"path to config.yaml (auto-created with built-in defaults if missing)")

	cmd.Flags().StringVarP(&flagSource, "source", "s", "", "source path (mounted volume)")
	cmd.Flags().StringVarP(&flagTarget, "target", "t", defaultTarget(), "target root directory")
	cmd.Flags().StringVarP(&flagName, "name", "n", "", "event name (single segment only)")
	cmd.Flags().StringVar(&flagDevice, "device", "", "force device id, skipping detection")
	cmd.Flags().StringVar(&flagFrom, "from", "", "force period start (YYYY-MM-DD), forces single segment")
	cmd.Flags().StringVar(&flagTo, "to", "", "force period end (YYYY-MM-DD), forces single segment")
	cmd.Flags().StringVar(&flagTemplate, "template", defaultTemplate, "path template")
	cmd.Flags().StringVar(&flagDBPath, "db", defaultDB(), "history database path")
	cmd.Flags().IntVar(&flagGapDays, "gap-days", -1, "consecutive day gap to merge into one segment (overrides config.yaml)")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "preview only, do not copy")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "accept all prompts (auto-pick highest, accept detected device)")
	cmd.Flags().BoolVar(&flagOverwrite, "overwrite", false, "auto-overwrite when target file exists with different content (default: prompt; with --yes: skip)")
	cmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output")

	cmd.AddCommand(versionCmd(), devicesCmd())
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println("ingest", version)
		},
	}
}

func devicesCmd() *cobra.Command {
	c := &cobra.Command{Use: "devices", Short: "Manage device rules"}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured device rules",
		RunE: func(_ *cobra.Command, _ []string) error {
			path, err := expandPath(flagDevicesPath)
			if err != nil {
				return err
			}
			rules, err := device.LoadOrInit(path)
			if err != nil {
				return err
			}
			fmt.Printf("(规则来自 %s)\n", path)
			for _, r := range rules {
				fmt.Printf("  %-12s %s (%s)\n", r.ID, r.Name, r.Manufacturer)
			}
			return nil
		},
	})
	return c
}

func runIngest(cmd *cobra.Command, _ []string) error {
	io := prompt.NewStdio(os.Stdin, os.Stdout)

	// 启动横幅。让用户清楚现在是哪个工具的哪个模式，并预告 4 步流程。
	fmt.Fprintf(io.Out, "ingest %s — 智能素材导入向导\n", version)
	fmt.Fprintln(io.Out, "================================================")
	if flagDryRun {
		fmt.Fprintln(io.Out, "（dry-run 模式：仅预览，不实际拷贝）")
	}

	devicesPath, err := expandPath(flagDevicesPath)
	if err != nil {
		return fmt.Errorf("--devices: %w", err)
	}
	configPath, err := expandPath(flagConfigPath)
	if err != nil {
		return fmt.Errorf("--config: %w", err)
	}
	settings, err := config.LoadOrInit(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	gapDays := settings.GapDays
	if flagGapDays >= 0 {
		gapDays = flagGapDays
	}
	rules, err := device.LoadOrInit(devicesPath)
	if err != nil {
		return fmt.Errorf("load devices: %w", err)
	}
	if len(rules) == 0 {
		return fmt.Errorf("%s 里没有任何设备规则——至少加一个条目", devicesPath)
	}

	// [1/4] 源选择
	fmt.Fprintln(io.Out, "\n[1/4] 选择源")
	src, err := resolveSource(io, rules)
	if err != nil {
		return err
	}

	// [2/4] 设备识别
	fmt.Fprintln(io.Out, "\n[2/4] 确认设备")
	deviceID, deviceName, err := resolveDevice(io, src, rules)
	if err != nil {
		return err
	}
	fmt.Fprintf(io.Out, "  → %s (%s)\n", deviceName, deviceID)

	// [3/4] 扫描 + 分段
	fmt.Fprintln(io.Out, "\n[3/4] 扫描素材并按事件分段")
	files, err := scanner.Scan(src)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("在 %s 下没找到媒体文件", src)
	}
	var totalSrcBytes int64
	for _, f := range files {
		totalSrcBytes += f.Size
	}
	fmt.Fprintf(io.Out, "  扫描到 %d 个文件 (%s)，源: %s\n",
		len(files), prompt.HumanBytes(totalSrcBytes), src)

	byPath := make(map[string]scanner.File, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}

	segments, err := buildSegments(files, gapDays)
	if err != nil {
		return err
	}
	if flagVerbose {
		fmt.Fprintf(io.Out, "  分成 %d 段 (gap_days=%d)\n", len(segments), gapDays)
	}

	plans, err := planSegments(io, segments, byPath)
	if err != nil {
		return err
	}

	// [4/4] 目标 + 总览确认 + 拷贝
	fmt.Fprintln(io.Out, "\n[4/4] 确认目标并开始拷贝")
	target, err := resolveTarget(io, cmd)
	if err != nil {
		return err
	}

	// 计算每段的目标路径 + 字节数，组装 summary 给用户最终确认
	planTargets := make([]string, len(plans))
	planBytes := make([]int64, len(plans))
	summarySegs := make([]string, len(plans))
	totalPlanBytes := int64(0)
	totalPlanFiles := 0
	for i, plan := range plans {
		rendered, err := template.Render(flagTemplate, template.Context{
			DateStart:  plan.Start.Format("20060102"),
			DateEnd:    optionalEnd(plan.Period()),
			EventName:  plan.Name,
			DeviceID:   deviceID,
			DeviceName: strings.ReplaceAll(deviceName, " ", "_"),
		})
		if err != nil {
			return fmt.Errorf("template (segment %d): %w", i+1, err)
		}
		planTargets[i] = filepath.Join(target, rendered)
		var b int64
		for _, f := range plan.Files {
			b += f.Size
		}
		planBytes[i] = b
		totalPlanBytes += b
		totalPlanFiles += len(plan.Files)

		dateLabel := plan.Start.Format("2006-01-02")
		if !plan.End.IsZero() && !plan.Start.Equal(plan.End) {
			dateLabel = fmt.Sprintf("%s→%s", plan.Start.Format("2006-01-02"), plan.End.Format("2006-01-02"))
		}
		summarySegs[i] = fmt.Sprintf("%s  %s  (%d 个文件, %s) → %s",
			dateLabel, plan.Name, len(plan.Files), prompt.HumanBytes(b), planTargets[i])
	}

	if !flagYes {
		ok, err := io.ConfirmProceed(prompt.ProceedSummary{
			Target:     target,
			Device:     deviceName,
			TotalFiles: totalPlanFiles,
			TotalBytes: totalPlanBytes,
			Segments:   summarySegs,
			DryRun:     flagDryRun,
		})
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(io.Out, "已取消。")
			return nil
		}
	}

	dbPath, err := expandPath(flagDBPath)
	if err != nil {
		return fmt.Errorf("--db: %w", err)
	}
	var store *db.DB
	if !flagDryRun {
		store, err = db.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open db: %w", err)
		}
		defer store.Close()
	}

	var totalCopied, totalSkipped, totalFailed int
	var totalBytes int64
	startAll := time.Now()
	// 整次 ingest 共享一个 trash 桶；每段冲突的旧文件都按 base name 平铺到这里。
	// 用 - 而不是 : 隔开时分秒，兼容 Windows 文件名规则。
	runTrashDir := filepath.Join(target, ".ingest-trash", startAll.Format("2006-01-02T15-04-05"))
	policy := overwritePolicy()
	for i, plan := range plans {
		targetDir := planTargets[i]
		fmt.Fprintf(io.Out, "\n[第 %d/%d 段] %s\n", i+1, len(plans), targetDir)

		if flagDryRun {
			for _, f := range plan.Files {
				fmt.Fprintf(io.Out, "  [dry-run] %s → %s\n",
					f.RelPath, filepath.Join(targetDir, filepath.Base(f.Path)))
			}
			continue
		}

		copied, skipped, failed, bytes := copyFiles(io, plan.Files, targetDir, deviceID, runTrashDir, policy, store)
		totalCopied += copied
		totalSkipped += skipped
		totalFailed += failed
		totalBytes += bytes
	}

	if flagDryRun {
		return nil
	}
	elapsed := time.Since(startAll).Round(time.Millisecond)
	mbps := 0.0
	if elapsed > 0 {
		mbps = float64(totalBytes) / elapsed.Seconds() / (1024 * 1024)
	}
	fmt.Fprintf(io.Out, "\n完成: %d 个已拷贝, %d 个跳过, %d 个失败, 用时 %s (%.1f MB/s)\n",
		totalCopied, totalSkipped, totalFailed, elapsed, mbps)
	if totalFailed > 0 {
		return fmt.Errorf("%d 个文件失败", totalFailed)
	}
	return nil
}

// resolveTarget 决定本次的目标根目录。优先级：
//   - --target 显式给出 → 直接展开使用
//   - --yes 时 → 静默用默认值（不 prompt）
//   - 其它情况（默认零参数交互）→ 弹 AskTarget，回车接受默认
func resolveTarget(io prompt.IO, cmd *cobra.Command) (string, error) {
	if cmd.Flags().Changed("target") || flagYes {
		return expandPath(flagTarget)
	}
	defaultDir, err := expandPath(flagTarget)
	if err != nil {
		return "", err
	}
	picked, err := io.AskTarget(defaultDir)
	if err != nil {
		return "", err
	}
	return expandPath(picked)
}

// segmentPlan 是一个段经过用户确认后的最终拷贝计划。
type segmentPlan struct {
	Start, End time.Time
	Name       string
	Files      []scanner.File
}

func (p segmentPlan) Period() period.Period {
	return period.Period{Start: p.Start, End: p.End}
}

// buildSegments 把 scanner.File 转成 period.File，按 gapDays 分段。
// 当 --from/--to 给出时，强制单段（保持向后兼容旧 CLI 行为）。
func buildSegments(files []scanner.File, gapDays int) ([]period.Segment, error) {
	if flagFrom != "" || flagTo != "" {
		var start, end time.Time
		var err error
		if flagFrom != "" {
			start, err = time.ParseInLocation("2006-01-02", flagFrom, time.Local)
			if err != nil {
				return nil, fmt.Errorf("--from: %w", err)
			}
		}
		if flagTo != "" {
			end, err = time.ParseInLocation("2006-01-02", flagTo, time.Local)
			if err != nil {
				return nil, fmt.Errorf("--to: %w", err)
			}
		}
		if start.IsZero() {
			start = end
		}
		if end.IsZero() {
			end = start
		}
		pfiles := scannerToPeriod(files)
		seg := period.Segment{Start: start, End: end, Files: pfiles}
		for _, f := range files {
			if f.Info != nil {
				seg.Bytes += f.Info.Size()
			}
		}
		return []period.Segment{seg}, nil
	}

	segs, stats := period.Segments(scannerToPeriod(files), gapDays)
	if flagVerbose {
		fmt.Printf("(时间来源: exif=%d quicktime=%d mtime 兜底=%d)\n",
			stats.FromExif, stats.FromQuickTime, stats.FromMtime)
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("无法从文件中推断出任何时间段")
	}
	return segs, nil
}

func scannerToPeriod(files []scanner.File) []period.File {
	out := make([]period.File, 0, len(files))
	for _, f := range files {
		out = append(out, period.File{Path: f.Path, Info: f.Info})
	}
	return out
}

// planSegments 给每段补上事件名（来自 --name flag 或交互 prompt），并把
// period.File 列表对回 scanner.File（拷贝阶段需要 RelPath）。
// autoSegmentName 给无头模式（--yes）自动命名：单段沿用 --name（没有则用日期）；
// 多段一律日期命名，--name 有值时作前缀。
func autoSegmentName(seg period.Segment, multi bool) string {
	start := seg.Start.Format("20060102")
	end := seg.End.Format("20060102")
	datePart := start
	if end != start {
		datePart = start + "_" + end
	}
	if multi && flagName != "" {
		return flagName + "-" + datePart
	}
	if !multi && flagName != "" {
		return flagName
	}
	return datePart
}

func planSegments(io prompt.IO, segs []period.Segment, byPath map[string]scanner.File) ([]segmentPlan, error) {
	if len(segs) > 1 && flagName != "" && !flagYes {
		return nil, fmt.Errorf("自动检测到 %d 段，--name 无法分配给哪一段; "+
			"去掉 --name 让程序逐段询问名字，或用 --from/--to 强制单段，或加 --yes 自动命名", len(segs))
	}

	plans := make([]segmentPlan, 0, len(segs))
	for i, seg := range segs {
		var name string
		start, end := seg.Start, seg.End

		switch {
		case flagYes:
			// 无头模式：不 prompt，按规则自动命名。
			name = autoSegmentName(seg, len(segs) > 1)
		case len(segs) == 1 && flagName != "":
			name = flagName
		default:
			r, err := io.EditSegment(prompt.SegmentEdit{
				Index: i + 1, Total: len(segs),
				Start: seg.Start, End: seg.End,
				FileCount: len(seg.Files), Bytes: seg.Bytes,
			})
			if err != nil {
				return nil, fmt.Errorf("segment %d/%d: %w", i+1, len(segs), err)
			}
			name = r.Name
			start, end = r.Start, r.End
		}

		segFiles := make([]scanner.File, 0, len(seg.Files))
		for _, p := range seg.Files {
			if sf, ok := byPath[p.Path]; ok {
				segFiles = append(segFiles, sf)
			}
		}
		plans = append(plans, segmentPlan{
			Start: start, End: end, Name: name,
			Files: segFiles,
		})
	}
	return plans, nil
}

// resolveSource 决定要扫的源路径。优先级：显式 --source > 自动检测。
//
// 自动检测时枚举系统挂载点（mount.List），对每个候选跑 device.Detect 评分，
// 按置信度排序后：
//   - 0 个匹配 → 报错让用户手动指定
//   - 1 个匹配 → 直接采用
//   - N 个匹配 → 列表 + 交互式数字选择；--yes 时自动取最高分
func resolveSource(io prompt.IO, rules []device.Rule) (string, error) {
	if flagSource != "" {
		src, err := expandPath(flagSource)
		if err != nil {
			return "", fmt.Errorf("--source: %w", err)
		}
		if st, err := os.Stat(src); err != nil || !st.IsDir() {
			return "", fmt.Errorf("源不是目录: %s", src)
		}
		return src, nil
	}

	vols, err := mount.List()
	if err != nil {
		return "", fmt.Errorf("自动检测挂载点失败: %w (请用 --source 明确指定)", err)
	}
	type candidate struct {
		Volume     mount.Volume
		Match      *device.Match
		Confidence float64
	}
	var matches []candidate
	for _, v := range vols {
		m := device.Detect(rules, v.Path, v.Label)
		if m == nil {
			continue
		}
		matches = append(matches, candidate{Volume: v, Match: m, Confidence: m.Confidence})
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("没有匹配到任何设备规则的可移动卷; 请用 --source 明确指定")
	case 1:
		c := matches[0]
		fmt.Fprintf(io.Out, "自动检测到源: %s (%s, 置信度 %.2f, 来自 %s)\n",
			c.Volume.Path, c.Match.Rule.Name, c.Confidence, c.Match.Reason)
		return c.Volume.Path, nil
	}

	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].Confidence > matches[i].Confidence {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}
	if flagYes {
		c := matches[0]
		fmt.Fprintf(io.Out, "自动选定 (--yes): %s (%s, 置信度 %.2f)\n",
			c.Volume.Path, c.Match.Rule.Name, c.Confidence)
		return c.Volume.Path, nil
	}

	fmt.Fprintln(io.Out, "多个可移动卷匹配到设备规则:")
	for i, c := range matches {
		fmt.Fprintf(io.Out, "  [%d] %-40s %s (置信度 %.2f, %s)\n",
			i+1, c.Volume.Path, c.Match.Rule.Name, c.Confidence, c.Match.Reason)
	}
	options := make([]prompt.DeviceOption, 0, len(matches))
	for _, c := range matches {
		options = append(options, prompt.DeviceOption{
			ID: c.Volume.Path, Name: c.Match.Rule.Name, Manufacturer: c.Match.Rule.Manufacturer,
		})
	}
	idx, err := io.PickDevice(options) // 复用 PickDevice 的"输入 1..N"语义
	if err != nil {
		return "", err
	}
	return matches[idx].Volume.Path, nil
}

// resolveDevice 决定本次拷贝用哪个设备 ID/Name。优先级：
//   - --device 显式给出 → 直接用（在 rules 里查 name；查不到时用 id 兜底）
//   - 自动 detect 命中 → 默认 prompt 用户确认；--yes 跳过 prompt
//   - prompt 选 list → 列出全部 rules 让用户选
//   - 自动 detect 未命中 → 提示加 --device 并退出
func resolveDevice(io prompt.IO, src string, rules []device.Rule) (id, name string, err error) {
	if flagDevice != "" {
		for _, r := range rules {
			if r.ID == flagDevice {
				return r.ID, r.Name, nil
			}
		}
		return flagDevice, flagDevice, nil
	}
	label := volumeLabelOf(src)
	m := device.Detect(rules, src, label)
	if m == nil {
		return "", "", fmt.Errorf("无法自动识别设备; 请用 --device 明确指定")
	}
	if flagYes {
		return m.Rule.ID, m.Rule.Name, nil
	}
	choice, err := io.ConfirmDevice(m.Rule.Name, m.Rule.ID, m.Reason, m.Confidence)
	if err != nil {
		return "", "", err
	}
	switch choice {
	case prompt.DeviceAccept:
		return m.Rule.ID, m.Rule.Name, nil
	case prompt.DeviceReject:
		return "", "", fmt.Errorf("设备已拒绝; 请重新运行并加 --device <id> 覆盖")
	case prompt.DeviceList:
		opts := make([]prompt.DeviceOption, 0, len(rules))
		for _, r := range rules {
			opts = append(opts, prompt.DeviceOption{ID: r.ID, Name: r.Name, Manufacturer: r.Manufacturer})
		}
		idx, err := io.PickDevice(opts)
		if err != nil {
			return "", "", err
		}
		return rules[idx].ID, rules[idx].Name, nil
	}
	return "", "", fmt.Errorf("未处理的设备选择")
}

// conflictPolicy 决定 copyFiles 遇到 ResultConflict 时的处理：
//   - policyAsk: prompt 用户每个冲突文件（默认）
//   - policyOverwrite: 全部覆盖（旧文件移到 trashDir）
//   - policySkip: 全部跳过冲突，保留旧目标
type conflictPolicy int

const (
	policyAsk conflictPolicy = iota
	policyOverwrite
	policySkip
)

// overwritePolicy 把 CLI flag 组合解释成具体策略。
//   - --overwrite              → policyOverwrite
//   - --yes 且 没 --overwrite  → policySkip（保守：自动模式下不覆盖未知数据）
//   - 其余                     → policyAsk
func overwritePolicy() conflictPolicy {
	if flagOverwrite {
		return policyOverwrite
	}
	if flagYes {
		return policySkip
	}
	return policyAsk
}

func copyFiles(io prompt.IO, files []scanner.File, targetDir, deviceID, trashDir string, policy conflictPolicy, store *db.DB) (copied, skipped, failed int, totalBytes int64) {
	for _, f := range files {
		dst := filepath.Join(targetDir, filepath.Base(f.Path))
		out := copier.SafeCopy(f.Path, dst, deviceID, "", store)

		if out.Result == copier.ResultConflict {
			out = handleConflict(io, f, dst, deviceID, trashDir, policy, store, out)
		}

		switch out.Result {
		case copier.ResultCopied:
			copied++
			totalBytes += out.Bytes
			if flagVerbose {
				fmt.Fprintf(io.Out, "  已拷贝  %s (%s)\n", f.RelPath, out.Hash)
			}
		case copier.ResultSkipped:
			skipped++
			if flagVerbose {
				fmt.Fprintf(io.Out, "  跳过    %s\n", f.RelPath)
			}
		case copier.ResultFailed:
			failed++
			fmt.Fprintf(os.Stderr, "  失败    %s: %v\n", f.RelPath, out.Err)
		}
	}
	return
}

// handleConflict 把一次 ResultConflict 转化成最终的 Copied/Skipped/Failed Outcome。
// policy 决定动作；其中 policyAsk 走 prompt（用户输入解析失败也按"保留旧目标"处理）。
func handleConflict(io prompt.IO, f scanner.File, dst, deviceID, trashDir string, policy conflictPolicy, store *db.DB, conflict copier.Outcome) copier.Outcome {
	overwrite := false
	switch policy {
	case policyOverwrite:
		overwrite = true
	case policySkip:
		fmt.Fprintf(os.Stderr, "  冲突    %s (目标已存在但内容不同；--yes 模式下保留旧目标，加 --overwrite 可覆盖)\n", f.RelPath)
		return copier.Outcome{Result: copier.ResultSkipped, Hash: conflict.SrcHash, Bytes: conflict.Bytes}
	case policyAsk:
		ok, err := io.ConfirmOverwrite(prompt.ConflictInfo{
			RelPath:   f.RelPath,
			TrashPath: filepath.Join(trashDir, filepath.Base(dst)),
			SrcSize:   conflict.Bytes, DstSize: conflict.DstSize,
			SrcHash: conflict.SrcHash, DstHash: conflict.DstHash,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  冲突    %s: %v (保留旧目标)\n", f.RelPath, err)
			return copier.Outcome{Result: copier.ResultSkipped}
		}
		overwrite = ok
	}

	if !overwrite {
		if flagVerbose {
			fmt.Fprintf(io.Out, "  保留    %s (用户选择保留旧目标)\n", f.RelPath)
		}
		return copier.Outcome{Result: copier.ResultSkipped}
	}
	return copier.OverwriteCopy(f.Path, dst, deviceID, "", trashDir, store)
}

func volumeLabelOf(p string) string {
	return filepath.Base(strings.TrimRight(p, string(os.PathSeparator)))
}

func optionalEnd(p period.Period) string {
	if p.IsSingleDay() {
		return ""
	}
	return p.End.Format("20060102")
}

// expandPath 把用户在 CLI 上输入的任意风格路径标准化：
//   - 展开开头的 ~（当前用户家目录）
//   - 展开 $VAR 形式的环境变量
//   - 解析为绝对路径并 Clean
//
// Windows 上 filepath 包原生处理 `\`、盘符、UNC 路径，无需额外分支。
// `%VAR%` 风格的 Windows 环境变量不展开（与 Go 标准做法一致；
// 若需要，文档建议改写成 `$VAR`）。
func expandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		p = filepath.Join(home, strings.TrimLeft(p[1:], `/\`))
	}
	p = os.ExpandEnv(p)
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func defaultTarget() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Backups")
}

func defaultDB() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "ingest", "ingest.db")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "ingest", "ingest.db")
}
