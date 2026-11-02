//go:build windows

package dialog

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func pickPath(ctx context.Context, kind string) (string, error) {
	var dialog string
	switch kind {
	case "folder":
		dialog = `
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = '选择行情 CSV 保存目录'
$dialog.ShowNewFolderButton = $true
`
	case "file":
		dialog = `
$dialog = New-Object System.Windows.Forms.OpenFileDialog
$dialog.Title = '选择通达信 connect.cfg'
$dialog.FileName = 'connect.cfg'
$dialog.Filter = '通达信配置 (*.cfg)|*.cfg|所有文件 (*.*)|*.*'
$dialog.CheckFileExists = $true
$dialog.Multiselect = $false
`
	case "gbbq":
		dialog = `
$dialog = New-Object System.Windows.Forms.OpenFileDialog
$dialog.Title = '选择通达信权息因子 gbbq'
$dialog.FileName = 'gbbq'
$dialog.Filter = '权息因子 (gbbq)|gbbq|所有文件 (*.*)|*.*'
$dialog.CheckFileExists = $true
$dialog.Multiselect = $false
`
	default:
		return "", fmt.Errorf("未知选择框类型 %q", kind)
	}
	script := `
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.ShowInTaskbar = $false
` + dialog + `
$result = $dialog.ShowDialog($owner)
if ($result -eq [System.Windows.Forms.DialogResult]::OK) {
    if ($dialog -is [System.Windows.Forms.FolderBrowserDialog]) { Write-Output $dialog.SelectedPath }
    else { Write-Output $dialog.FileName }
}
$dialog.Dispose()
$owner.Dispose()
`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-STA", "-Command", script)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("选择框已关闭或等待超时")
	}
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return "", fmt.Errorf("无法打开系统选择框：%w：%s", err, detail)
		}
		return "", fmt.Errorf("无法打开系统选择框：%w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
