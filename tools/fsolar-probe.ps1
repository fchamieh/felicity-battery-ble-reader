# fsolar-probe.ps1 -- query the Felicity battery over its WiFi hotspot,
# tunneled through the phone via adb (phone-side pipe, which is the only
# stdin path that works reliably). One short-lived TCP connection per command.
#
# The battery is a DHCP client on its own AP at 192.168.155.1, port 53970.
# Protocol (from the decompiled app, e8/d.java): raw UTF-8 command string out,
# unframed JSON object(s) back, no handshake, no auth.
#
# Usage:
#   pwsh tools/fsolar-probe.ps1 -Cmd "wifilocalMonitor:get dev basice infor"
#   pwsh tools/fsolar-probe.ps1 -Cmd "wifilocalMonitor:get dev real infor" -WaitSec 8
#   pwsh tools/fsolar-probe.ps1 -Cmd "wifilocalMonitor:get Date"
#   pwsh tools/fsolar-probe.ps1 -All        # run every known get command
#
# Output: the raw JSON to the console; with -SaveDir, one .json file per command.
param(
    [string]$HostIp = '192.168.155.1',
    [int]$Port = 53970,
    [string]$Cmd = '',
    [int]$WaitSec = 12,
    [string]$SaveDir = '',
    [switch]$All
)
$ErrorActionPreference = 'Stop'
$adb = 'C:\Program Files (x86)\DroidCam\adb\adb.exe'

# The known monitor "get" commands (from a8/r.java).
$allCmds = [ordered]@{
    'basice' = 'wifilocalMonitor:get dev basice infor'
    'real'   = 'wifilocalMonitor:get dev real infor'
    'set'    = 'wifilocalMonitor:get dev set infor'
    'test'   = 'wifilocalMonitor:get dev test infor'
    'date'   = 'wifilocalMonitor:get Date'
}

function Send-Command([string]$Cmd, [string]$Tag) {
    # Phone-side pipe: echo the command | nc to the battery, capture to /tmp.
    $sh = "echo -n `"$Cmd`" | nc -w $WaitSec $HostIp $Port > /tmp/fsolar_$Tag.bin 2>&1"
    $p = Start-Process -FilePath $adb -ArgumentList 'shell', $sh -NoNewWindow -PassThru
    if (-not $p.WaitForExit(($WaitSec + 25) * 1000)) { $p.Kill(); Write-Warning "$Tag : probe timed out" }

    $out = & $adb exec-out "cat /tmp/fsolar_$Tag.bin" 2>$null
    $text = ($out | Out-String).Trim()
    if ($SaveDir) {
        if (-not (Test-Path $SaveDir)) { New-Item -ItemType Directory -Path $SaveDir | Out-Null }
        [System.IO.File]::WriteAllText((Join-Path $SaveDir "$Tag.json"), $text)
    }
    return $text
}

if ($All) {
    foreach ($kv in $allCmds.GetEnumerator()) {
        Write-Host ("=== {0} :: {1} ===" -f $kv.Key, $kv.Value)
        $r = Send-Command $kv.Value $kv.Key
        Write-Host $r
        Write-Host ''
    }
}
elseif ($Cmd) {
    $tag = $Cmd -replace '[^a-zA-Z0-9]+', '_'
    $r = Send-Command $Cmd $tag
    Write-Host $r
}
else {
    Write-Host "Specify -Cmd `"<command>`" or -All. Known commands:"
    $allCmds.GetEnumerator() | ForEach-Object { Write-Host ("  {0,-7} {1}" -f $_.Key, $_.Value) }
    exit 1
}
