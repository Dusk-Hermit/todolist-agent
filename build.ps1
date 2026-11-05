<#
.SYNOPSIS
  构建 TodoList Agent 单文件可执行程序与发布包。
.DESCRIPTION
  需要 Go 1.26+、WebView2 Runtime、rsrc。首次构建会自动安装 rsrc。
  产物: dist\TodoListAgent.exe, dist\TodoListAgent-win64.zip
#>
[CmdletBinding()]
param(
  [string]$Version = "0.2.9"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

# --- 定位 Go ---------------------------------------------------------------
$localGo = Join-Path $env:LOCALAPPDATA "go-toolchain\go\bin\go.exe"
if (Test-Path $localGo) {
  $env:GOROOT = Join-Path $env:LOCALAPPDATA "go-toolchain\go"
  $env:PATH = "$env:GOROOT\bin;" + $env:PATH
  $go = $localGo
} else {
  $cmd = Get-Command go -ErrorAction SilentlyContinue
  if (-not $cmd) { throw "未找到 Go。请安装 Go 1.26+ 并加入 PATH。" }
  $go = $cmd.Source
}

if (-not $env:GOPROXY) { $env:GOPROXY = "https://goproxy.cn,direct" }
$env:CGO_ENABLED = "0"
$env:GOTOOLCHAIN = "auto"

Write-Host "==> Go: $(& $go version)"

# --- 版本一致性检查 ---------------------------------------------------------
# appVersion / installer / about 页面各自带着版本号；构建时先校验，避免发布
# 一个 exe 是 0.2.9、卸载信息却还是 0.2.5 的版本。
$modelVersion = (Select-String -Path "models.go" -Pattern 'appVersion\s*=\s*"([^"]+)"').Matches[0].Groups[1].Value
$installVersion = (Select-String -Path "install.ps1" -Pattern 'DisplayVersion" -Value "([^"]+)"').Matches[0].Groups[1].Value
$webVersion = (Select-String -Path "web\index.html" -Pattern 'aboutVersion">([^<]+)<').Matches[0].Groups[1].Value
if ($Version -ne $modelVersion -or $Version -ne $installVersion -or $Version -ne $webVersion) {
  throw "版本不一致: build=$Version models=$modelVersion install=$installVersion web=$webVersion"
}
Write-Host "==> 版本: $Version"

# --- 图标工具 --------------------------------------------------------------
$rsrc = Join-Path $env:USERPROFILE "go\bin\rsrc.exe"
if (-not (Test-Path $rsrc)) {
  Write-Host "==> 安装 rsrc ..."
  & $go install github.com/akavel/rsrc@latest
  if (-not (Test-Path $rsrc)) { throw "rsrc 安装失败" }
}

# --- 生成 Windows 资源 ------------------------------------------------------
Write-Host "==> 生成图标与 manifest 资源 ..."
& $rsrc -ico "assets\icon.ico" -manifest "assets\app.manifest" -arch amd64 -o "rsrc_windows_amd64.syso"
if ($LASTEXITCODE -ne 0) { throw "rsrc 失败" }

# --- 停止正在运行的实例，避免文件占用 -------------------------------------
Get-Process TodoListAgent -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 400

# --- 编译 -------------------------------------------------------------------
Write-Host "==> 编译 ..."
New-Item -ItemType Directory -Force "dist" | Out-Null
Remove-Item "dist\TodoListAgent.exe~" -Force -ErrorAction SilentlyContinue
& $go build -trimpath -ldflags "-s -w -H windowsgui" -o "dist\TodoListAgent.exe" .
if ($LASTEXITCODE -ne 0) { throw "go build 失败" }

# --- 组装发布目录 -----------------------------------------------------------
Copy-Item "install.ps1", "install.cmd", "uninstall.ps1", "uninstall.cmd", "README.md" "dist\" -Force
Copy-Item "assets\icon.ico" "dist\icon.ico" -Force
if (Test-Path "docs") {
  if (Test-Path "dist\docs") { Remove-Item "dist\docs" -Recurse -Force }
  Copy-Item "docs" "dist\docs" -Recurse -Force
}
Get-ChildItem "dist" -Filter "*.pdb" -ErrorAction SilentlyContinue | Remove-Item -Force

$zip = "dist\TodoListAgent-win64.zip"
Remove-Item $zip -Force -ErrorAction SilentlyContinue
$zipItems = @(
  "dist\TodoListAgent.exe",
  "dist\icon.ico",
  "dist\install.ps1",
  "dist\install.cmd",
  "dist\uninstall.ps1",
  "dist\uninstall.cmd",
  "dist\README.md"
)
if (Test-Path "dist\docs") { $zipItems += "dist\docs" }
Compress-Archive -Path $zipItems -DestinationPath $zip -CompressionLevel Optimal

$exe = Get-Item "dist\TodoListAgent.exe"
Write-Host ""
Write-Host "构建完成:" -ForegroundColor Green
Write-Host ("  {0}  ({1:N0} bytes)" -f $exe.FullName, $exe.Length)
Write-Host ("  {0}" -f (Resolve-Path $zip))
Write-Host ""
Write-Host "安装: 右键 install.ps1 -> 使用 PowerShell 运行，或运行 install.cmd"
