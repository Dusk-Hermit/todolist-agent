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
Get-Process TodoListAgent -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 500

Write-Host "==> 安装到 $dest ..."
New-Item -ItemType Directory -Force $dest | Out-Null
Copy-Item $srcExe $destExe -Force
Copy-Item (Join-Path $src "uninstall.ps1") $dest -Force
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
$lnk.IconLocation = "$destExe,0"
$lnk.Description = "TodoList Agent - 与 DeepSeek Harness 当前会话联动的桌面清单"
$lnk.Save()
Write-Host "==> 开始菜单: $shortcutPath"

if ($Desktop) {
  $desktopShortcut = Join-Path ([Environment]::GetFolderPath("Desktop")) "TodoList Agent.lnk"
  $dlnk = $ws.CreateShortcut($desktopShortcut)
  $dlnk.TargetPath = $destExe
  $dlnk.WorkingDirectory = $dest
  $dlnk.IconLocation = "$destExe,0"
  $dlnk.Description = "TodoList Agent - DSH 联动清单"
  $dlnk.Save()
  Write-Host "==> 桌面快捷方式: $desktopShortcut"
}

# --- 卸载注册表 -------------------------------------------------------------
$uninstallKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\TodoListAgent"
New-Item -Path $uninstallKey -Force | Out-Null
$sizeKB = [int]((Get-Item $destExe).Length / 1KB) + 64
Set-ItemProperty -Path $uninstallKey -Name "DisplayName" -Value "TodoList Agent"
Set-ItemProperty -Path $uninstallKey -Name "DisplayVersion" -Value "0.2.0"
Set-ItemProperty -Path $uninstallKey -Name "Publisher" -Value "TodoList Agent"
Set-ItemProperty -Path $uninstallKey -Name "InstallLocation" -Value $dest
Set-ItemProperty -Path $uninstallKey -Name "DisplayIcon" -Value "$destExe,0"
Set-ItemProperty -Path $uninstallKey -Name "EstimatedSize" -Value $sizeKB -Type DWord
Set-ItemProperty -Path $uninstallKey -Name "NoModify" -Value 1 -Type DWord
Set-ItemProperty -Path $uninstallKey -Name "NoRepair" -Value 1 -Type DWord
Set-ItemProperty -Path $uninstallKey -Name "UninstallString" -Value "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$dest\uninstall.ps1`""
Set-ItemProperty -Path $uninstallKey -Name "QuietUninstallString" -Value "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$dest\uninstall.ps1`" -Quiet"

Write-Host "==> 启动 TodoList Agent ..."
Start-Process -FilePath $destExe -WorkingDirectory $dest

Write-Host ""
Write-Host "安装完成。" -ForegroundColor Green
Write-Host "  - 开始菜单: TodoList Agent"
Write-Host "  - 卸载: 设置 -> 应用 -> TodoList Agent，或运行 $dest\uninstall.ps1"
Write-Host "  - 数据: $env:APPDATA\TodoListAgent"
