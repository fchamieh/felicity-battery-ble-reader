# probe_oneline.ps1 -- phone-side pipe probe (reliable stdin path).
param(
    [string]$HostIp = '192.168.155.1',
    [int]$Port = 53970,
    [string]$Cmd = 'wifilocalMonitor:get dev basice infor',
    [bool]$Newline = $false,
    [int]$WaitSec = 12
)
$ErrorActionPreference = 'Stop'
$adb = 'C:\Program Files (x86)\DroidCam\adb\adb.exe'

$nl = if ($Newline) { '; echo' } else { '' }
$sh = "echo -n `"$Cmd`"$nl | nc -w $WaitSec $HostIp $Port > /tmp/r.bin 2>&1"
$p = Start-Process -FilePath $adb -ArgumentList 'shell', $sh -NoNewWindow -PassThru
if (-not $p.WaitForExit(($WaitSec + 25) * 1000)) { $p.Kill(); Write-Host 'probe TIMED OUT' }
Write-Host ("probe done (exit {0})" -f $p.ExitCode)

$h = & $adb exec-out 'hexdump -C /tmp/r.bin' 2>&1
Write-Host '--- response bytes ---'
$h | ForEach-Object { Write-Host $_ }
