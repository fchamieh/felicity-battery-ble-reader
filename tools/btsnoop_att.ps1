function BE32([byte[]]$b, [int]$o) {
    return ([uint32]$b[$o] -shl 24) -bor ([uint32]$b[$o + 1] -shl 16) -bor
           ([uint32]$b[$o + 2] -shl 8) -bor [uint32]$b[$o + 3]
}
function LE16([byte[]]$b, [int]$o) { return [int]$b[$o] -bor ([int]$b[$o + 1] -shl 8) }

# Android's btsnoop (datalink 1002) writes each HCI ACL packet as
#   indicator(1) | connection handle+flags(2, LE) | acl_length(2, LE) | data
# but a long L2CAP/ATT message is split over several ACL packets: only the
# first one still carries the L2CAP header (length + CID 0x0004), the rest are
# raw continuation bytes. We detect the header by looking for CID 4.
$b = [System.IO.File]::ReadAllBytes((Resolve-Path .\capture\btsnoop_hci.log))
$pos = 16
$msgs = New-Object System.Collections.ArrayList
$aclCount = 0

while ($pos + 24 -le $b.Length) {
    $incl = BE32 $b ($pos + 4); $dir = BE32 $b ($pos + 8) -band 1; $pos += 24
    if ($incl -le 0 -or $pos + $incl -gt $b.Length) { break }
    $p = New-Object byte[] $incl; [Array]::Copy($b, $pos, $p, 0, $incl); $pos += $incl
    if ($p[0] -ne 2) { continue }
    $aclCount++
    $aclLen = LE16 $p 3
    $avail = [Math]::Min($aclLen, $p.Length - 5)
    if ($avail -le 0) { continue }

    if ($avail -ge 4 -and (LE16 $p 7) -eq 4) {
        # first fragment: L2CAP length at [5], CID at [7], ATT PDU from [9]
        $l2cap = LE16 $p 5
        $pduLen = [Math]::Min($l2cap, $avail - 4)
        $op = $p[9]
        $attr = if ($pduLen -ge 3) { LE16 $p 10 } else { 0 }
        $body = New-Object byte[] ([Math]::Max(0, $pduLen - 3))
        if ($body.Length -gt 0) { [Array]::Copy($p, 12, $body, 0, $body.Length) }
        [void]$msgs.Add([pscustomobject]@{ Dir = $dir; Op = $op; Attr = $attr; Body = $body })
    } else {
        # continuation of the previous message in the same direction
        $prev = $null
        for ($i = $msgs.Count - 1; $i -ge 0; $i--) { if ($msgs[$i].Dir -eq $dir) { $prev = $msgs[$i]; break } }
        if ($null -eq $prev) { continue }
        $extra = New-Object byte[] $avail
        [Array]::Copy($p, 5, $extra, 0, $avail)
        $merged = New-Object byte[] ($prev.Body.Length + $avail)
        [Array]::Copy($prev.Body, 0, $merged, 0, $prev.Body.Length)
        [Array]::Copy($extra, 0, $merged, $prev.Body.Length, $avail)
        $prev.Body = $merged
    }
}

Write-Host ('ACL packets: {0}   ATT messages after reassembly: {1}' -f $aclCount, $msgs.Count)
Write-Host ''
$opNames = @{ 1='Exchange MTU Req'; 2='Exchange MTU Rsp'; 8='Read By Type Rsp';
              9='Read Req'; 10='Read Rsp'; 11='Write Req'; 12='Write Rsp';
              17='Read By Group Type Req'; 18='Read By Group Type Rsp';
              27='Handle Value Notification'; 29='Handle Value Indication';
              82='Write Command' }
Write-Host '--- ATT message mix ---'
$msgs | Group-Object { '{0}  {1}  attr 0x{2:X}' -f $_.Dir, $(if ($opNames.ContainsKey($_.Op)) { $opNames[$_.Op] } else { 'op 0x{0:X2}' -f $_.Op }), $_.Attr } |
    Sort-Object -Descending Count | ForEach-Object { Write-Host ('  {0,3} x  dir={1}' -f $_.Count, $_.Name) }

Write-Host ''
Write-Host '--- everything written to the battery ---'
foreach ($m in $msgs) {
    if ($m.Op -eq 0x52 -or $m.Op -eq 0x12 -or $m.Op -eq 0x09) {
        $t = [System.Text.Encoding]::UTF8.GetString($m.Body)
        Write-Host ('  op 0x{0:X2} ({1})  attr 0x{2:X}  {3} bytes' -f $m.Op, $(if ($opNames.ContainsKey($m.Op)) { $opNames[$m.Op] } else { '?' }), $m.Attr, $m.Body.Length)
        Write-Host ('      ' + $t)
    }
}

$notes = @($msgs | Where-Object { $_.Op -eq 0x1B -or $_.Op -eq 0x1D })
Write-Host ''
Write-Host '--- notifications (battery -> host) ---'
if (-not $notes.Count) { Write-Host '  none found'; exit }
$m1 = $notes | Measure-Object -Property { $_.Body.Length } -Minimum -Maximum -Sum
Write-Host ('  count {0}   min {1} B   max {2} B   total {3} B' -f $notes.Count, $m1.Minimum, $m1.Maximum, $m1.Sum)
Write-Host ('  characteristic handles seen: ' + (($notes | ForEach-Object { '0x{0:X}' -f $_.Attr } | Sort-Object -Unique) -join ' '))
$notes | Group-Object { $_.Body.Length } | Sort-Object { [int]$_.Name } | ForEach-Object {
    Write-Host ('  {0,5} B x {1}' -f [int]$_.Name, $_.Count)
}
Write-Host ''
Write-Host '--- where the JSON is actually cut ---'
$i = 0
foreach ($n in $notes) {
    if ($i -ge 8) { break }
    Write-Host ('  [{0}] {1,4} B | {2}' -f $i, $n.Body.Length, [System.Text.Encoding]::UTF8.GetString($n.Body))
    $i++
}
Write-Host ''
$sb = New-Object System.Text.StringBuilder
foreach ($n in $notes) { [void]$sb.Append([System.Text.Encoding]::UTF8.GetString($n.Body)) }
$all = $sb.ToString()
$depth = 0; $start = -1; $inStr = $false; $esc = $false; $docs = 0; $max = 0
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
        if ($depth -eq 0 -and $start -ge 0) {
            $docs++; $len = $i - $start + 1; if ($len -gt $max) { $max = $len }
            Write-Host ('  JSON doc {0}: {1} B | {2}' -f $docs, $len, $all.Substring($start, [Math]::Min(50, $len)))
            $start = -1
        }
    }
}
Write-Host ('  {0} complete JSON documents reassembled, largest {1} bytes' -f $docs, $max)
