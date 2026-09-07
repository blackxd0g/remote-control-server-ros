param([string]$Docker = 'docker')
$ErrorActionPreference = 'Stop'
$project = Split-Path $PSScriptRoot -Parent
$work = Join-Path $project 'work'
New-Item -ItemType Directory -Force $work | Out-Null
$secretPath = Join-Path $work 'test-internal.secret'
$secret = [Guid]::NewGuid().ToString('N') + [Guid]::NewGuid().ToString('N')
[IO.File]::WriteAllText($secretPath, $secret)
$suffix = [Guid]::NewGuid().ToString('N').Substring(0,8)
$network = "rcr-smoke-$suffix"
$api = "rcr-api-$suffix"
$relay = "rcr-relay-$suffix"
function DockerRun([string[]]$Arguments) {
    $result = & $Docker @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Docker command failed: $($Arguments[0])" }
    return $result
}
$report = @()
try {
    DockerRun @('network','create',$network) | Out-Null
    DockerRun @('run','-d','--name',$api,'--network',$network,'-p','127.0.0.1:23414:21114','--entrypoint=/usr/local/bin/art-api',
      '-e',"RDS_INTERNAL_SECRET=$secret",'-e','RDS_BOOTSTRAP_ADMIN_USERNAME=relaytest-admin',
      '-e',"RDS_BOOTSTRAP_ADMIN_PASSWORD=$([Guid]::NewGuid().ToString('N'))",
      'blackxdog/remote-control-server-ros:2.2.1@sha256:e1ad1e4a46601d935082114d21e09892915376c20f6449dba9605a67eef447b8') | Out-Null
    $headers = @{'X-RDS-Internal-Token'=$secret}
    $ready = $false
    for ($i=0; $i -lt 30; $i++) {
        try { $snapshot = Invoke-RestMethod 'http://127.0.0.1:23414/internal/v1/auth/snapshot' -Headers $headers -TimeoutSec 2; $ready=$true; break } catch { Start-Sleep -Seconds 1 }
    }
    if (!$ready) { throw 'Isolated API did not start' }
    foreach ($arch in @('amd64','arm64')) {
        $relay = "rcr-relay-$arch-$suffix"
        DockerRun @('run','-d','--rm','--platform',"linux/$arch",'--name',$relay,'--network',$network,
          '--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--pids-limit=128',
          '-p','127.0.0.1:23417:21117/tcp','-p','127.0.0.1:23419:21119/tcp','-p','127.0.0.1:23419:21119/udp',
          '--mount',"type=bind,source=$secretPath,target=/run/secrets/internal.secret,readonly",
          '-e',"RDS_API_INTERNAL_URL=http://${api}:21114",'-e',"RDS_HBBR_ID=smoke-$arch",
          '-e',"RDS_HBBR_PUBLIC_ADDRESS=${relay}:21117",'-e','RDS_HBBR_NAME=Smoke relay',
          '-e','RDS_HBBR_TELEMETRY_INTERVAL=2','-e','RUST_LOG=debug','remote-control-relay:2.2.1') | Out-Null
        Start-Sleep -Seconds 3
        $probe = DockerRun @('run','--rm','--network',$network,
          '--mount',"type=bind,source=$PSScriptRoot,target=/tests,readonly",
          '--mount',"type=bind,source=$secretPath,target=/secret,readonly",
          '-e','RCR_SECRET_FILE=/secret','-e',"RCR_HOST=$relay",'-e','RCR_TCP_PORT=21117',
          '-e','RCR_WS_PORT=21119','-e','RCR_CONTROL_PORT=21119',
          '-e',"RCR_API_URL=http://${api}:21114",'-e',"RCR_RELAY_ID=smoke-$arch",
          'node:24-alpine','node','/tests/probe.mjs')
        DockerRun @('cp',"${relay}:/art-hbbr",(Join-Path $work "art-hbbr-$arch")) | Out-Null
        $bytes = [IO.File]::ReadAllBytes((Join-Path $work "art-hbbr-$arch"))
        $machine = [BitConverter]::ToUInt16($bytes,18)
        $expected = if ($arch -eq 'amd64') { 62 } else { 183 }
        if ($machine -ne $expected) { throw "Wrong ELF architecture: $machine" }
        $stats = DockerRun @('stats','--no-stream','--format','{{.MemUsage}}',$relay)
        $report += @{architecture=$arch; elf_machine=$machine; binary_bytes=$bytes.Length; protocol=($probe -join "`n" | ConvertFrom-Json); telemetry_registered=$true; memory=$stats}
        DockerRun @('stop',$relay) | Out-Null
    }
    $report | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $work 'smoke-result.json')
    $report | ConvertTo-Json -Depth 8
} catch {
    & $Docker logs --tail 30 $relay 2>&1 | Write-Output
    & $Docker logs --tail 15 $api 2>&1 | Write-Output
    throw
} finally {
    & $Docker rm -f -v $relay $api 2>$null | Out-Null
    & $Docker network rm $network 2>$null | Out-Null
    Remove-Item Env:RCR_SECRET_FILE -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $secretPath -ErrorAction SilentlyContinue
}
