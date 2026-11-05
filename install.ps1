<#
.SYNOPSIS
  安装 TodoList Agent 到当前用户目录，并创建开始菜单快捷方式。
.DESCRIPTION
  不需要管理员权限。安装位置: %LOCALAPPDATA%\Programs\TodoList Agent
  卸载信息写入 HKCU，可在“设置 -> 应用”中卸载。
.PARAMETER Desktop
  同时创建桌面快捷方式。
#>
[CmdletBinding()]
param(
  [switch]$Desktop
)

$ErrorActionPreference = "Stop"
$src = Split-Path -Parent $MyInvocation.MyCommand.Path
$exeName = "TodoListAgent.exe"
$srcExe = Join-Path $src $exeName
if (-not (Test-Path $srcExe)) { throw "找不到 $exeName。请在解压后的发布目录中运行安装脚本。" }

$dest = Join-Path $env:LOCALAPPDATA "Programs\TodoList Agent"
$destExe = Join-Path $dest $exeName

Write-Host "==> 关闭正在运行的 TodoList Agent ..."
$running = @(Get-Process TodoListAgent -ErrorAction SilentlyContinue)
foreach ($p in $running) {
  # Ask the old window to close first so DWM does not keep its old icon
  # thumbnail in Alt+Tab while the update is being copied.
  try { $null = $p.CloseMainWindow() } catch {}
}
if ($running.Count -gt 0) { Start-Sleep -Milliseconds 900 }
Get-Process TodoListAgent -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 1200

Write-Host "==> 安装到 $dest ..."
New-Item -ItemType Directory -Force $dest | Out-Null
Copy-Item $srcExe $destExe -Force
Copy-Item (Join-Path $src "uninstall.ps1") $dest -Force

# Use a standalone icon next to the exe. This gives Explorer a new icon path
# instead of reusing the stale blue icon cached for the old exe/AUMID.
$srcIcon = Join-Path $src "icon.ico"
if (-not (Test-Path $srcIcon)) { $srcIcon = Join-Path $src "assets\icon.ico" }
$destIcon = Join-Path $dest "icon.ico"
if (Test-Path $srcIcon) { Copy-Item $srcIcon $destIcon -Force }
$iconLocation = if (Test-Path $destIcon) { "$destIcon,0" } else { "$destExe,0" }
if (Test-Path (Join-Path $src "README.md")) { Copy-Item (Join-Path $src "README.md") $dest -Force }
if (Test-Path (Join-Path $src "docs")) {
  $destDocs = Join-Path $dest "docs"
  if (Test-Path $destDocs) { Remove-Item $destDocs -Recurse -Force }
  Copy-Item (Join-Path $src "docs") $destDocs -Recurse -Force
}

# --- 开始菜单快捷方式 -------------------------------------------------------
$startMenu = [Environment]::GetFolderPath("Programs")
$shortcutPath = Join-Path $startMenu "TodoList Agent.lnk"
$ws = New-Object -ComObject WScript.Shell
$lnk = $ws.CreateShortcut($shortcutPath)
$lnk.TargetPath = $destExe
$lnk.WorkingDirectory = $dest
$lnk.IconLocation = $iconLocation
$lnk.Description = "TodoList Agent - 与 DeepSeek Harness 当前会话联动的桌面清单"
$lnk.Save()
Write-Host "==> 开始菜单: $shortcutPath"

if ($Desktop) {
  $desktopShortcut = Join-Path ([Environment]::GetFolderPath("Desktop")) "TodoList Agent.lnk"
  $dlnk = $ws.CreateShortcut($desktopShortcut)
  $dlnk.TargetPath = $destExe
  $dlnk.WorkingDirectory = $dest
  $dlnk.IconLocation = $iconLocation
  $dlnk.Description = "TodoList Agent - DSH 联动清单"
  $dlnk.Save()
  Write-Host "==> 桌面快捷方式: $desktopShortcut"
}

# --- 卸载注册表 -------------------------------------------------------------
$uninstallKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\TodoListAgent"
New-Item -Path $uninstallKey -Force | Out-Null
$sizeKB = [int]((Get-Item $destExe).Length / 1KB) + 64
Set-ItemProperty -Path $uninstallKey -Name "DisplayName" -Value "TodoList Agent"
Set-ItemProperty -Path $uninstallKey -Name "DisplayVersion" -Value "0.3.0"
Set-ItemProperty -Path $uninstallKey -Name "Publisher" -Value "TodoList Agent"
Set-ItemProperty -Path $uninstallKey -Name "InstallLocation" -Value $dest
Set-ItemProperty -Path $uninstallKey -Name "DisplayIcon" -Value $iconLocation
Set-ItemProperty -Path $uninstallKey -Name "EstimatedSize" -Value $sizeKB -Type DWord
Set-ItemProperty -Path $uninstallKey -Name "NoModify" -Value 1 -Type DWord
Set-ItemProperty -Path $uninstallKey -Name "NoRepair" -Value 1 -Type DWord
Set-ItemProperty -Path $uninstallKey -Name "UninstallString" -Value "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$dest\uninstall.ps1`""
Set-ItemProperty -Path $uninstallKey -Name "QuietUninstallString" -Value "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$dest\uninstall.ps1`" -Quiet"

# The old build used a blue icon at the same exe path. Explorer can keep that
# image in its icon cache even after the file is replaced, so explicitly ask the
# shell to re-read the shortcut/icon path before launching the new build.
try {
  Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class TodoListShellRefresh {
  [DllImport("shell32.dll")]
  public static extern void SHChangeNotify(int eventId, uint flags, IntPtr item1, IntPtr item2);
}
'@ -ErrorAction SilentlyContinue
  [TodoListShellRefresh]::SHChangeNotify(0x08000000, 0x0000, [IntPtr]::Zero, [IntPtr]::Zero)
} catch {}
try {
  $ie4uinit = Join-Path $env:SystemRoot "System32\ie4uinit.exe"
  if (Test-Path $ie4uinit) {
    Start-Process -FilePath $ie4uinit -ArgumentList "-show" -WindowStyle Hidden -Wait -ErrorAction SilentlyContinue
  }
} catch {}

Write-Host "==> 启动 TodoList Agent ..."
Start-Process -FilePath $destExe -WorkingDirectory $dest

Write-Host ""
Write-Host "安装完成。" -ForegroundColor Green
Write-Host "  - 开始菜单: TodoList Agent"
Write-Host "  - 卸载: 设置 -> 应用 -> TodoList Agent，或运行 $dest\uninstall.ps1"
Write-Host "  - 数据: $env:APPDATA\TodoListAgent"
