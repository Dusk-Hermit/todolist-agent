param(
  [Parameter(Mandatory)][string]$Ndk,
  [Parameter(Mandatory)][string]$Output
)
$ErrorActionPreference = "Stop"
$localGo = Join-Path $env:LOCALAPPDATA "go-toolchain\go\bin\go.exe"
$go = if (Test-Path $localGo) { $localGo } else { (Get-Command go.exe).Source }
$llvm = Join-Path $Ndk "toolchains\llvm\prebuilt\windows-x86_64\bin"
$env:GOOS = "android"
$env:CGO_ENABLED = "1"
$env:CGO_LDFLAGS = "-Wl,-z,max-page-size=16384"
$targets = @(
  @{ ABI = "arm64-v8a"; Arch = "arm64"; Triple = "aarch64-linux-android" },
  @{ ABI = "x86_64"; Arch = "amd64"; Triple = "x86_64-linux-android" }
)
foreach ($target in $targets) {
  $env:GOARCH = $target.Arch
  $compiler = Join-Path $llvm ($target.Triple + "26-clang.cmd")
  # CGO launches the compiler directly, so use clang.exe with the target
  # option rather than relying on the NDK's command-shell wrapper.
  if (-not (Test-Path $compiler)) { throw "Missing NDK compiler: $compiler" }
  $clang = Join-Path $llvm "clang.exe"
  $env:CC = '"' + $clang + '" --target=' + $target.Triple + '26'
  $abiDir = Join-Path $Output $target.ABI
  New-Item -ItemType Directory -Force $abiDir | Out-Null
  & $go build -trimpath -buildmode=c-shared -ldflags="-s -w" -o (Join-Path $abiDir "libtodoagent.so") .
  if ($LASTEXITCODE -ne 0) { throw "Go Android build failed for $($target.ABI)" }
}
