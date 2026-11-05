# Replays capture\btsnoop_hci.log, reassembles the ATT traffic and writes every
# complete JSON document to capture\reassembled.jsonl (one per line).
#
# Two layers of splitting have to be undone:
#   1. one ATT message is fragmented over several small ACL packets;
#   2. one JSON document is then split over several *separate* notification
#      PDUs - which is what the app's StringBuffer and fsolar's JSONStream both
#      handle.
#
# Android's btsnoop (datalink 1002) stores each HCI ACL packet as
#   indicator(1) | handle+flags(2, LE) | acl_length(2, LE) | data
# and only the first fragment of a message still carries the L2CAP header
# (length, then CID 0x0004 for ATT). Continuations are raw bytes.
function BE32([byte[]]$b, [int]$o) {
    return ([uint32]$b[$o] -shl 24) -bor ([uint32]$b[$o + 1] -shl 16) -bor
           ([uint32]$b[$o + 2] -shl 8) -bor [uint32]$b[$o + 3]
}
function LE16([byte[]]$b, [int]$o) { return [int]$b[$o] -bor ([int]$b[$o + 1] -shl 8) }

$b = [System.IO.File]::ReadAllBytes((Resolve-Path .\capture\btsnoop_hci.log))
$pos = 16
$msgs = New-Object System.Collections.ArrayList
$fragments = 0

while ($pos + 24 -le $b.Length) {
    $incl = BE32 $b ($pos + 4)
    $hostToDev = (BE32 $b ($pos + 8) -band 1) -eq 0   # btsnoop flag bit 0: 0 = host -> device
    $pos += 24
    if ($incl -le 0 -or $pos + $incl -gt $b.Length) { break }
    $p = New-Object byte[] $incl; [Array]::Copy($b, $pos, $p, 0, $incl); $pos += $incl
    if ($p[0] -ne 2) { continue }
    $aclLen = LE16 $p 3
    $avail = [Math]::Min($aclLen, $p.Length - 5)
    if ($avail -le 0) { continue }

    if ($avail -ge 4 -and (LE16 $p 7) -eq 4) {
        $l2cap = LE16 $p 5
        $pduLen = [Math]::Min($l2cap, $avail - 4)
        if ($pduLen -lt 1) { continue }
        $body = New-Object byte[] ([Math]::Max(0, $pduLen - 3))
        if ($body.Length -gt 0) { [Array]::Copy($p, 12, $body, 0, $body.Length) }
        [void]$msgs.Add([pscustomobject]@{
            Dir = $(if ($hostToDev) { 'TX' } else { 'RX' }); Op = $p[9]
            Attr = $(if ($pduLen -ge 3) { LE16 $p 10 } else { 0 }); Body = $body
        })
    } elseif ($msgs.Count -gt 0) {
        # continuation of the previous message in the same direction
        for ($i = $msgs.Count - 1; $i -ge 0; $i--) {
            if ($msgs[$i].Dir -eq $(if ($hostToDev) { 'TX' } else { 'RX' })) {
                $prev = $msgs[$i]
                $extra = New-Object byte[] $avail
                [Array]::Copy($p, 5, $extra, 0, $avail)
                $merged = New-Object byte[] ($prev.Body.Length + $avail)
                [Array]::Copy($prev.Body, 0, $merged, 0, $prev.Body.Length)
                [Array]::Copy($extra, 0, $merged, $prev.Body.Length, $avail)
                $prev.Body = $merged
                $fragments++
                break
            }
        }
    }
}

$rx = @($msgs | Where-Object { $_.Dir -eq 'RX' -and ($_.Op -eq 0x1B -or $_.Op -eq 0x1D) })
$tx = @($msgs | Where-Object { $_.Dir -eq 'TX' -and ($_.Op -eq 0x52 -or $_.Op -eq 0x12) })

Write-Host ('ATT messages: {0}   of which continuation fragments: {1}' -f $msgs.Count, $fragments)
Write-Host ('writes to the battery: {0}   notifications from it: {1}' -f $tx.Count, $rx.Count)
Write-Host ''
Write-Host '--- commands written to the battery ---'
foreach ($w in $tx) {
    Write-Host ('  op 0x{0:X2} char handle 0x{1:X}  payload {2} B' -f $w.Op, $w.Attr, $w.Body.Length)
    Write-Host ('    ' + [System.Text.Encoding]::UTF8.GetString($w.Body).Trim())
}
Write-Host ''
Write-Host '--- notification sizes ---'
$m = $rx | Measure-Object -Property { $_.Body.Length } -Minimum -Maximum -Sum
Write-Host ('  count {0}   min {1} B   max {2} B   total {3} B' -f $rx.Count, $m.Minimum, $m.Maximum, $m.Sum)

$sb = New-Object System.Text.StringBuilder
foreach ($n in $rx) { [void]$sb.Append([System.Text.Encoding]::UTF8.GetString($n.Body)) }
$all = $sb.ToString()

$docs = New-Object System.Collections.ArrayList
$depth = 0; $start = -1; $inStr = $false; $esc = $false
for ($i = 0; $i -lt $all.Length; $i++) {
    $c = $all[$i]
    if ($inStr) {
        if ($esc) { $esc = $false } elseif ($c -eq '\') { $esc = $true } elseif ($c -eq '"') { $inStr = $false }
        continue
    }
    if ($c -eq '"') { $inStr = $true }
    elseif ($c -eq '{') { if ($depth -eq 0) { $start = $i }; $depth++ }
    elseif ($c -eq '}') {
        $depth--
        if ($depth -eq 0 -and $start -ge 0) { [void]$docs.Add($all.Substring($start, $i - $start + 1)); $start = -1 }
    }
}

$docs | Set-Content -Path .\capture\reassembled.jsonl -Encoding utf8NoBOM
Write-Host ''
Write-Host '--- complete JSON documents recovered ---'
foreach ($d in $docs) { Write-Host ('  {0,5} B | {1}' -f $d.Length, $d.Substring(0, [Math]::Min(72, $d.Length))) }
Write-Host ('wrote {0} documents to capture\reassembled.jsonl' -f $docs.Count)
