// Package notify 发 Windows toast 通知（ingest watch 用）。
// 非 Windows 下退化为 stdout 一行，不引入任何依赖。
package notify

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// Toast 发一条通知。title/body 里的双引号会被转义。
func Toast(title, body string) {
	if runtime.GOOS != "windows" {
		fmt.Printf("[notify] %s: %s\n", title, body)
		return
	}
	script := BuildToastScript(title, body)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	cmd.Stdout = os.Stdout
	// 通知失败不影响主流程，只打印。
	if err := cmd.Run(); err != nil {
		fmt.Printf("[notify] %s: %s (toast failed: %v)\n", title, body, err)
	}
}

// BuildToastScript 拼 Windows.UI.Notifications 的内联 PS（Win10+ 自带，无需模块）。
func BuildToastScript(title, body string) string {
	esc := func(s string) string {
		out := ""
		for _, r := range s {
			if r == '"' {
				out += "`\""
			} else {
				out += string(r)
			}
		}
		return out
	}
	return fmt.Sprintf(`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null; $t = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02); $t.GetElementsByTagName("text")[0].AppendChild($t.CreateTextNode("%s")) | Out-Null; $t.GetElementsByTagName("text")[1].AppendChild($t.CreateTextNode("%s")) | Out-Null; [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("ingest").Show($t)`,
		esc(title), esc(body))
}
