param(
  [string]$OutDir = "dist-sea",
  [string]$ExeName = "deerflow-tui.exe"
)

$ErrorActionPreference = "Stop"

$nodePath = (Get-Command node.exe).Source
$targetDir = Resolve-Path $OutDir
$targetExe = Join-Path $targetDir $ExeName
$blobPath = Join-Path $targetDir "deerflow-tui.blob"
$postjectPath = Join-Path (Resolve-Path ".") "node_modules\.bin\postject.cmd"

if (!(Test-Path $blobPath)) {
  throw "SEA blob not found: $blobPath"
}

if (!(Test-Path $postjectPath)) {
  throw "postject not found. Run: pnpm.cmd install"
}

Copy-Item -LiteralPath $nodePath -Destination $targetExe -Force

$signtool = Get-Command signtool.exe -ErrorAction SilentlyContinue
if ($signtool) {
  & $signtool.Source remove /s $targetExe | Out-Null
}

& $postjectPath `
  $targetExe `
  NODE_SEA_BLOB `
  $blobPath `
  --sentinel-fuse NODE_SEA_FUSE_fce680ab2cc467b6e072b8b5df1996b2

Write-Host "Created $targetExe"
