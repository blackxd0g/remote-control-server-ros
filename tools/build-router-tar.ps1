param(
    [Parameter(Mandatory)] [string] $BaseTar,
    [Parameter(Mandatory)] [string] $LayerBlob,
    [Parameter(Mandatory)] [string] $LayerDiffID,
    [Parameter(Mandatory)] [string] $OutputTar,
    [string] $Tag = 'blackxdog/rustdesk-server-routeros:router'
)

$ErrorActionPreference = 'Stop'
$base = (Resolve-Path -LiteralPath $BaseTar).Path
$layer = (Resolve-Path -LiteralPath $LayerBlob).Path
$output = [IO.Path]::GetFullPath($OutputTar)
$stage = Join-Path ([IO.Path]::GetDirectoryName($output)) ('.router-repack-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage | Out-Null

function Read-JsonFile([string] $Path) {
    return Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
}

function Write-JsonFile([string] $Path, $Value) {
    $json = $Value | ConvertTo-Json -Depth 100 -Compress
    [IO.File]::WriteAllText($Path, $json, [Text.UTF8Encoding]::new($false))
}

function File-SHA256([string] $Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

tar -xf $base -C $stage
if ($LASTEXITCODE -ne 0) { throw 'Unable to extract the base OCI archive' }

$indexPath = Join-Path $stage 'index.json'
$legacyPath = Join-Path $stage 'manifest.json'
$index = Read-JsonFile $indexPath
$oldManifestDigest = $index.manifests[0].digest.Substring(7)
$oldManifestPath = Join-Path $stage "blobs/sha256/$oldManifestDigest"
$manifest = Read-JsonFile $oldManifestPath
$oldConfigDigest = $manifest.config.digest.Substring(7)
$oldConfigPath = Join-Path $stage "blobs/sha256/$oldConfigDigest"
$config = Read-JsonFile $oldConfigPath

# The Go/Web binary is the third layer in the verified all-in-one image.
$layerIndex = 2
$newLayerDigest = File-SHA256 $layer
$newLayerPath = Join-Path $stage "blobs/sha256/$newLayerDigest"
Copy-Item -LiteralPath $layer -Destination $newLayerPath
$manifest.layers[$layerIndex].digest = "sha256:$newLayerDigest"
$manifest.layers[$layerIndex].size = (Get-Item -LiteralPath $layer).Length
$manifest.layers[$layerIndex].mediaType = 'application/vnd.oci.image.layer.v1.tar+gzip'
$config.rootfs.diff_ids[$layerIndex] = $LayerDiffID

$temporaryConfig = Join-Path $stage 'config.new.json'
Write-JsonFile $temporaryConfig $config
$newConfigDigest = File-SHA256 $temporaryConfig
$newConfigPath = Join-Path $stage "blobs/sha256/$newConfigDigest"
Move-Item -LiteralPath $temporaryConfig -Destination $newConfigPath
$manifest.config.digest = "sha256:$newConfigDigest"
$manifest.config.size = (Get-Item -LiteralPath $newConfigPath).Length

$temporaryManifest = Join-Path $stage 'manifest.new.json'
Write-JsonFile $temporaryManifest $manifest
$newManifestDigest = File-SHA256 $temporaryManifest
$newManifestPath = Join-Path $stage "blobs/sha256/$newManifestDigest"
Move-Item -LiteralPath $temporaryManifest -Destination $newManifestPath
$index.manifests[0].digest = "sha256:$newManifestDigest"
$index.manifests[0].size = (Get-Item -LiteralPath $newManifestPath).Length
$index.manifests[0].annotations.'config.digest' = "sha256:$newConfigDigest"
$index.manifests[0].annotations.'io.containerd.image.name' = "docker.io/$Tag"
$index.manifests[0].annotations.'org.opencontainers.image.ref.name' = $Tag.Split(':')[-1]
Write-JsonFile $indexPath $index

$legacy = Read-JsonFile $legacyPath
$legacy[0].Config = "blobs/sha256/$newConfigDigest"
$legacy[0].RepoTags = @($Tag)
$legacy[0].Layers[$layerIndex] = "blobs/sha256/$newLayerDigest"
$legacyJson = ConvertTo-Json -InputObject @($legacy) -Depth 100 -Compress
[IO.File]::WriteAllText($legacyPath, $legacyJson, [Text.UTF8Encoding]::new($false))

tar -cf $output -C $stage blobs index.json manifest.json oci-layout
if ($LASTEXITCODE -ne 0) { throw 'Unable to create the RouterOS OCI archive' }
Write-Output $output
