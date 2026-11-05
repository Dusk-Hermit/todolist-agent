<#
.SYNOPSIS
  卸载 TodoList Agent。
.PARAMETER PurgeData
  同时删除 %APPDATA%\TodoListAgent 中的清单、历史和本地桥接凭据。
.PARAMETER Quiet
  不显示最终提示（供“应用和功能”调用）。
#>
[CmdletBinding()]
param(
  [switch]$PurgeData,
  [switch]$Quiet
)

$ErrorActionPreference = "Stop"
# Do not keep the install directory as this process's CWD: the delayed
# rmdir at the end must be able to remove it.
Set-Location $env:SystemRoot
$dest = Join-Path $env:LOCALAPPDATA "Programs\TodoList Agent"
$exe = Join-Path $dest "TodoListAgent.exe"

Write-Host "==> 关闭 TodoList Agent ..."
Get-Process TodoListAgent -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 600

# --- 快捷方式 ---------------------------------------------------------------
$startMenu = [Environment]::GetFolderPath("Programs")
$shortcutPath = Join-Path $startMenu "TodoList Agent.lnk"
if (Test-Path $shortcutPath) { Remove-Item $shortcutPath -Force }
$desktopShortcut = Join-Path ([Environment]::GetFolderPath("Desktop")) "TodoList Agent.lnk"
if (Test-Path $desktopShortcut) { Remove-Item $desktopShortcut -Force }

# --- 注册表 -----------------------------------------------------------------
$uninstallKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\TodoListAgent"
if (Test-Path $uninstallKey) { Remove-Item $uninstallKey -Recurse -Force }

if ($PurgeData) {
  $data = Join-Path $env:APPDATA "TodoListAgent"
  if (Test-Path $data) {
    Write-Host "==> 删除数据目录 $data ..."
    Remove-Item $data -Recurse -Force -ErrorAction SilentlyContinue
  }
}

# --- 删除安装目录（脚本自身也在其中，延迟到进程退出后） --------------------
if (Test-Path $dest) {
  Write-Host "==> 删除安装目录 $dest ..."
  $cmd = "timeout /t 2 /nobreak >nul & rmdir /s /q `"$dest`""
  Start-Process -WindowStyle Hidden -FilePath $env:ComSpec -ArgumentList "/c", $cmd
}

if (-not $Quiet) {
  Write-Host ""
  Write-Host "TodoList Agent 已卸载。" -ForegroundColor Green
  if (-not $PurgeData) {
    Write-Host "清单与历史仍保留在: $env:APPDATA\TodoListAgent"
    Write-Host "如需彻底清除，请重新运行本脚本并加 -PurgeData。"
  }
  Write-Host ""
}
