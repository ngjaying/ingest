package template

import (
	"fmt"
	"strings"
)

type Context struct {
	DateStart  string
	DateEnd    string
	EventName  string
	DeviceID   string
	DeviceName string
	// DatePath/DatePathEnd：年/年月/年月日 层级路径（如 2026/202609/20260910）。
	// 与 hilight reorg 的落盘结构对齐。
	DatePath    string
	DatePathEnd string
}

// Render expands a template string. Variables use {name} and optional segments
// use [..]; an optional segment is omitted entirely if any variable inside it
// resolves to an empty string. Example:
//
//	"{date_start}[_{date_end}]-{event_name}/origin-{device_id}"
func Render(tmpl string, ctx Context) (string, error) {
	vars := map[string]string{
		"date_start":   ctx.DateStart,
		"date_end":     ctx.DateEnd,
		"event_name":   ctx.EventName,
		"device_id":    ctx.DeviceID,
		"device_name":  ctx.DeviceName,
		"date_path":    ctx.DatePath,
		"date_path_end": ctx.DatePathEnd,
	}
	var b strings.Builder
	for i := 0; i < len(tmpl); {
		switch tmpl[i] {
		case '[':
			end := indexByte(tmpl, i+1, ']')
			if end < 0 {
				return "", fmt.Errorf("unclosed '[' at %d", i)
			}
			seg, ok, err := renderSegment(tmpl[i+1:end], vars)
			if err != nil {
				return "", err
			}
			if ok {
				b.WriteString(seg)
			}
			i = end + 1
		case '{':
			end := indexByte(tmpl, i+1, '}')
			if end < 0 {
				return "", fmt.Errorf("unclosed '{' at %d", i)
			}
			key := tmpl[i+1 : end]
			v, found := vars[key]
			if !found {
				return "", fmt.Errorf("unknown variable: %s", key)
			}
			b.WriteString(v)
			i = end + 1
		default:
			b.WriteByte(tmpl[i])
			i++
		}
	}
	return sanitize(b.String()), nil
}

func renderSegment(seg string, vars map[string]string) (string, bool, error) {
	var b strings.Builder
	for i := 0; i < len(seg); {
		if seg[i] == '{' {
			end := indexByte(seg, i+1, '}')
			if end < 0 {
				return "", false, fmt.Errorf("unclosed '{' in segment")
			}
			key := seg[i+1 : end]
			v, found := vars[key]
			if !found {
				return "", false, fmt.Errorf("unknown variable in segment: %s", key)
			}
			if v == "" {
				return "", false, nil
			}
			b.WriteString(v)
			i = end + 1
		} else {
			b.WriteByte(seg[i])
			i++
		}
	}
	return b.String(), true, nil
}

func indexByte(s string, start int, c byte) int {
	for i := start; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

var illegal = map[rune]rune{
	'\\': '_', ':': '_', '*': '_', '?': '_',
	'"': '_', '<': '_', '>': '_', '|': '_',
}

// sanitize replaces filesystem-illegal characters but preserves '/' as a
// path separator so multi-component templates still produce nested directories.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if rep, bad := illegal[r]; bad {
			b.WriteRune(rep)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
