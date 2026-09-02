param(
    [Parameter(Mandatory)] [string] $BaseTar,
    [Parameter(Mandatory)] [string] $NewTar,
    [Parameter(Mandatory)] [string] $OutputTar,
    [string] $Tag = 'blackxdog/rustdesk-server-routeros:0.5.0'
)

$ErrorActionPreference = 'Stop'
$base = (Resolve-Path -LiteralPath $BaseTar).Path
$new = (Resolve-Path -LiteralPath $NewTar).Path
$output = [IO.Path]::GetFullPath($OutputTar)
$stage = Join-Path ([IO.Path]::GetDirectoryName($output)) ('.router-full-' + [guid]::NewGuid().ToString('N'))
$newStage = "$stage-new"
New-Item -ItemType Directory -Path $stage, $newStage | Out-Null

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

try {
    tar -xf $base -C $stage
    if ($LASTEXITCODE -ne 0) { throw 'Unable to extract the RouterOS base archive' }
    tar -xf $new -C $newStage
    if ($LASTEXITCODE -ne 0) { throw 'Unable to extract the new Docker archive' }

    $indexPath = Join-Path $stage 'index.json'
    $legacyPath = Join-Path $stage 'manifest.json'
    $index = Read-JsonFile $indexPath
    $oldManifestDigest = $index.manifests[0].digest.Substring(7)
    $manifest = Read-JsonFile (Join-Path $stage "blobs/sha256/$oldManifestDigest")
    $newLegacy = Read-JsonFile (Join-Path $newStage 'manifest.json')
    $newConfig = Read-JsonFile (Join-Path $newStage $newLegacy[0].Config)

    if ($manifest.layers.Count -ne $newLegacy[0].Layers.Count) {
        throw 'Base and new image layer counts differ'
    }

    # Layers 0-1 are the verified Alpine base. Layers 2-5 contain API/Web,
    # HBBS, HBBR and the entrypoint. Layer 6 is Docker metadata and unchanged.
    foreach ($layerIndex in 2..5) {
        $relativeLayer = $newLegacy[0].Layers[$layerIndex]
        $sourceLayer = Join-Path $newStage $relativeLayer
        $digest = File-SHA256 $sourceLayer
        Copy-Item -LiteralPath $sourceLayer -Destination (Join-Path $stage "blobs/sha256/$digest")
        $manifest.layers[$layerIndex].digest = "sha256:$digest"
        $manifest.layers[$layerIndex].size = (Get-Item -LiteralPath $sourceLayer).Length
        $manifest.layers[$layerIndex].mediaType = 'application/vnd.oci.image.layer.v1.tar+gzip'
    }

    $temporaryConfig = Join-Path $stage 'config.new.json'
    Write-JsonFile $temporaryConfig $newConfig
    $newConfigDigest = File-SHA256 $temporaryConfig
    Move-Item -LiteralPath $temporaryConfig -Destination (Join-Path $stage "blobs/sha256/$newConfigDigest")
    $manifest.config.digest = "sha256:$newConfigDigest"
    $manifest.config.size = (Get-Item -LiteralPath (Join-Path $stage "blobs/sha256/$newConfigDigest")).Length

    $temporaryManifest = Join-Path $stage 'manifest.new.json'
    Write-JsonFile $temporaryManifest $manifest
    $newManifestDigest = File-SHA256 $temporaryManifest
    Move-Item -LiteralPath $temporaryManifest -Destination (Join-Path $stage "blobs/sha256/$newManifestDigest")
    $index.manifests[0].digest = "sha256:$newManifestDigest"
    $index.manifests[0].size = (Get-Item -LiteralPath (Join-Path $stage "blobs/sha256/$newManifestDigest")).Length
    $index.manifests[0].annotations.'config.digest' = "sha256:$newConfigDigest"
    $index.manifests[0].annotations.'io.containerd.image.name' = "docker.io/$Tag"
    $index.manifests[0].annotations.'org.opencontainers.image.ref.name' = $Tag.Split(':')[-1]
    Write-JsonFile $indexPath $index

    $legacy = Read-JsonFile $legacyPath
    $legacy[0].Config = "blobs/sha256/$newConfigDigest"
    $legacy[0].RepoTags = @($Tag)
    foreach ($layerIndex in 2..5) {
        $legacy[0].Layers[$layerIndex] = $newLegacy[0].Layers[$layerIndex]
    }
    [IO.File]::WriteAllText($legacyPath, (ConvertTo-Json -InputObject @($legacy) -Depth 100 -Compress), [Text.UTF8Encoding]::new($false))

    tar -cf $output -C $stage blobs index.json manifest.json oci-layout
    if ($LASTEXITCODE -ne 0) { throw 'Unable to create the RouterOS archive' }
    Write-Output $output
}
finally {
    Remove-Item -Recurse -Force -LiteralPath $stage, $newStage -ErrorAction SilentlyContinue
}
