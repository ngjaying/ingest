package device

import (
	"os"
	"path/filepath"
	"strings"
)

type Rule struct {
	ID           string
	Name         string
	Manufacturer string
	VolumeLabels []string
	Directories  []string
	FilePatterns []string
	// Target 是该设备的专属目标根目录（watch 模式/无头用）。
	// 为空时回退到全局 --target。
	Target string
}

type Match struct {
	Rule       Rule
	Confidence float64
	Reason     string
}

// Detect 在给定规则集合中挑置信度最高的匹配；无匹配返回 nil。
// 规则来源由调用方决定（通常是 LoadOrInit 加载的 YAML 配置）。
func Detect(rules []Rule, volumePath, volumeLabel string) *Match {
	var best *Match
	for _, r := range rules {
		m := score(r, volumePath, volumeLabel)
		if m == nil {
			continue
		}
		if best == nil || m.Confidence > best.Confidence {
			best = m
		}
	}
	return best
}

func score(r Rule, root, label string) *Match {
	upper := strings.ToUpper(label)
	for _, lab := range r.VolumeLabels {
		if upper != "" && strings.Contains(upper, strings.ToUpper(lab)) {
			return &Match{Rule: r, Confidence: 0.9, Reason: "卷标"}
		}
	}
	dirHits := 0
	for _, d := range r.Directories {
		if st, err := os.Stat(filepath.Join(root, d)); err == nil && st.IsDir() {
			dirHits++
		}
	}
	if len(r.Directories) > 0 && dirHits == len(r.Directories) {
		return &Match{Rule: r, Confidence: 0.8, Reason: "目录结构"}
	}
	if matchesAny(r.FilePatterns, root) {
		return &Match{Rule: r, Confidence: 0.7, Reason: "文件名模式"}
	}
	// 部分目录命中：要求至少 2 个目录命中才回退到此档，避免单个偶然命中
	// （比如杂牌 U 盘上有个 DCIM）造成的 false positive。规则只配 1 个目录
	// 时不参与 partial 评分（要么全中，要么不中）。
	if dirHits >= 2 {
		return &Match{Rule: r, Confidence: 0.5 + 0.1*float64(dirHits), Reason: "部分目录"}
	}
	return nil
}

func matchesAny(patterns []string, root string) bool {
	for _, p := range patterns {
		for _, glob := range []string{
			filepath.Join(root, p),
			filepath.Join(root, "*", p),
			filepath.Join(root, "*", "*", p),
		} {
			if hits, _ := filepath.Glob(glob); len(hits) > 0 {
				return true
			}
		}
	}
	return false
}
