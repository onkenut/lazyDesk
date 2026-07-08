//go:build windows

package control

import (
	"log"
	"os/exec"
)

// PowerAction 电源操作
func (h *Handler) PowerAction(action string) {
	if !h.cfg.Power.Enabled {
		log.Println("Power actions are disabled in config")
		return
	}

	var cmd *exec.Cmd

	switch action {
	case "sleep":
		cmd = exec.Command("rundll32.exe", "powrprof.dll,SetSuspendState", "0", "1", "0")
	case "shutdown":
		cmd = exec.Command("shutdown", "/s", "/t", "0")
	case "lock":
		cmd = exec.Command("rundll32.exe", "user32.dll,LockWorkStation")
	case "monitor_off":
		cmd = exec.Command("powershell", "-NoProfile", "-Command",
			`Add-Type -Name Win32 -Namespace System -MemberDefinition '[DllImport("user32.dll")]public static extern int SendMessage(int hWnd, int Msg, int wParam, int lParam);'; [System.Win32]::SendMessage(-1, 0x0112, 0xF170, 2)`)
	default:
		log.Printf("Unknown power action: %s", action)
		return
	}

	log.Printf("Executing power action: %s", action)
	if err := cmd.Run(); err != nil {
		log.Printf("Power action %s failed: %v", action, err)
	}
}
