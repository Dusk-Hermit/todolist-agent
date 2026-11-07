param(
  [Parameter(Mandatory)][ValidatePattern('^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$')][string]$Tag,
  [Parameter(Mandatory)][string]$Directory
)

$ErrorActionPreference = 'Stop'
$version = $Tag.Substring(1)
$assets = foreach ($name in 'TodoListAgent-Setup-win64.exe', 'TodoListAgent-win64.zip', 'TodoListAgent-android.apk', "todolist-agent-dsh-connector-$version.tgz") {
  $file = Get-Item -LiteralPath (Join-Path $Directory $name)
  if ($file.PSIsContainer -or $file.Length -le 0 -or $file.Length -gt 128MB) {
    throw "Invalid release asset $name"
  }
  $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName).Hash.ToLowerInvariant()
  [ordered]@{ name = $name; size = $file.Length; digest = "sha256:$hash" }
}
$manifest = [ordered]@{ tag_name = $Tag; assets = @($assets) } | ConvertTo-Json -Depth 4
$output = Join-Path (Resolve-Path -LiteralPath $Directory).Path 'TodoListAgent-update.json'
[System.IO.File]::WriteAllText($output, $manifest, [System.Text.UTF8Encoding]::new($false))
Write-Output $output
