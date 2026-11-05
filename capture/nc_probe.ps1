# nc_probe.ps1 -- persistent TCP probe to the Felicity battery through the
# phone (adb exec-out nc). Mirrors the FSolar app exactly: one long-lived
# socket to 192.168.155.1:53970, raw UTF-8 command strings, JSON stream back.
param(
    [string]$HostIp = '192.168.155.1',
    [int]$Port = 53970,
    [string]$Cmds = 'wifilocalMonitor:get dev basice infor',
    [int]$Loops = 4,
    [double]$IntervalSec = 1.0,
    [int]$TailWaitSec = 3
)
$ErrorActionPreference = 'Stop'
$adb = 'C:\Program Files (x86)\DroidCam\adb\adb.exe'
$cmds = $Cmds -split ';;'

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $adb
$psi.Arguments = ("exec-out nc {0} {1}" -f $HostIp, $Port)
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.UseShellExecute = $false
$p = [System.Diagnostics.Process]::Start($psi)

$sb = New-Object System.Text.StringBuilder
$ro = $p.StandardOutput.BaseStream
$er = $p.StandardError.BaseStream
$sin = $p.StandardInput.BaseStream
$enc = [Text.Encoding]::UTF8

function Read-Available {
    # Drain whatever is waiting on stdout without blocking.
    while ($ro.Available -gt 0) {
        $b = New-Object byte[] $ro.Available
        $n = $ro.Read($b, 0, $b.Length)
        if ($n -le 0) { break }
        [void]$sb.Append([Text.Encoding]::UTF8.GetString($b, 0, $n))
    }
}

# wait for adb/nc to connect on the phone side
Start-Sleep -Milliseconds 1500

for ($loop = 0; $loop -lt $Loops; $loop++) {
    foreach ($c in $cmds) {
        $bytes = $enc.GetBytes($c)
        $sin.Write($bytes, 0, $bytes.Length)
        $sin.Flush()
        Write-Host (">> {0}" -f $c)
        Start-Sleep -Milliseconds ([int]($IntervalSec * 1000))
        Read-Available
    }
}
Write-Host ("... waiting {0}s for trailing data" -f $TailWaitSec)
Start-Sleep -Seconds $TailWaitSec
Read-Available
try { $sin.Close() } catch { }
Start-Sleep -Milliseconds 500
Read-Available

# drain stderr (non-blocking)
$errText = ''
while ($er.Available -gt 0) {
    $b = New-Object byte[] $er.Available
    $n = $er.Read($b, 0, $b.Length)
    if ($n -le 0) { break }
    $errText += [Text.Encoding]::UTF8.GetString($b, 0, $n)
}

try { $p.WaitForExit(1000) | Out-Null; if (-not $p.HasExited) { $p.Kill() } } catch { }

Write-Host ("================ received {0} chars ================" -f $sb.Length)
Write-Host $sb.ToString()
if ($errText.Length -gt 0) { Write-Host '----- stderr -----'; Write-Host $errText }
