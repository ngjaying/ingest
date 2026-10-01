// Package hook 批后处理：manifest 落盘 + 外部命令 Hook。
//
// 语义（需求拍板）：ingest 成功即成功；Hook 失败只记录，不影响本次导入结果，
// 更不会导致下次重导（历史库已提交）。
package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// FileRecord 单个文件的归档记录。
type FileRecord struct {
	Rel   string `json:"rel"`
	Dst   string `json:"dst"`
	Size  int64  `json:"size"`
	Hash  string `json:"hash"`
	Skip  bool   `json:"skip,omitempty"`
}

// Manifest 一次段拷贝的清单，写进段目录 manifest.json。
type Manifest struct {
	Device   string       `json:"device"`
	DeviceID string       `json:"device_id"`
	Target   string       `json:"target"`
	Finished string       `json:"finished"`
	Files    []FileRecord `json:"files"`
	Hooks    []HookResult `json:"hooks,omitempty"`
}

// HookResult 单条 hook 命令的执行结果（失败也只记录）。
type HookResult struct {
	Cmd  string `json:"cmd"`
	Code int    `json:"code"`
	Err  string `json:"err,omitempty"`
}

// WriteManifest 把清单写进 dir/manifest.json（原子写：tmp + rename）。
func WriteManifest(dir string, m Manifest) error {
	m.Finished = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "manifest.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "manifest.json"))
}

// Expand 展开命令里的 {var} 占位（变量名大小写敏感，未知变量原样保留）。
// 注意：展开后的路径不要再加引号——cmd 对引号的切分有坑，路径里本来就没有空格。
func Expand(cmd string, vars map[string]string) string {
	for k, v := range vars {
		cmd = strings.ReplaceAll(cmd, "{"+k+"}", v)
	}
	return cmd
}

// Run 执行一条 hook 命令（shell 语义）。返回码非零只记录，不抛错——
// 调用方必须把 Hook 失败当作记录处理，不能让它推翻导入结果。
func Run(cmd string) HookResult {
	res := HookResult{Cmd: cmd}
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		c = exec.Command("sh", "-c", cmd)
	}
	// 固定子进程 CWD：watch 常驻时 CWD 可能是 WSL UNC 路径，
	// cmd 会为此往 stderr 抱怨并可能被调用方误判为失败。
	c.Dir = os.TempDir()
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		res.Err = err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			res.Code = ee.ExitCode()
		} else {
			res.Code = -1
		}
	}
	return res
}

// RunAll 顺序执行 hooks，vars 展开后逐条跑，全部只记录不抛错。
func RunAll(cmds []string, vars map[string]string) []HookResult {
	out := make([]HookResult, 0, len(cmds))
	for _, raw := range cmds {
		cmd := Expand(raw, vars)
		fmt.Printf("  [hook] %s\n", cmd)
		out = append(out, Run(cmd))
	}
	return out
}
