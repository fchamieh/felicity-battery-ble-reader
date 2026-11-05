# Probe visible WiFi networks: SSID, signal, security (auth/privacy), connectable flag.
$ErrorActionPreference = 'Stop'

$code = @'
using System;
using System.Runtime.InteropServices;

public sealed class Wnet
{
    [DllImport("wlanapi.dll", EntryPoint = "WlanOpenHandle", SetLastError = true)]
    public static extern uint Open(uint v, IntPtr r, ref uint nv, out IntPtr h);

    [DllImport("wlanapi.dll", EntryPoint = "WlanCloseHandle")]
    public static extern void Close(IntPtr h, IntPtr r);

    [DllImport("wlanapi.dll", EntryPoint = "WlanEnumInterfaces", SetLastError = true)]
    public static extern uint Enum(IntPtr h, IntPtr r, ref IntPtr ppList);

    [DllImport("wlanapi.dll", EntryPoint = "WlanFreeMemory")]
    public static extern void Free(IntPtr p);

    [DllImport("wlanapi.dll", EntryPoint = "WlanGetAvailableNetworkList", SetLastError = true)]
    public static extern uint Avail(IntPtr h, uint reserved, uint flags,
        IntPtr pInterfaceGuid, ref uint count, ref uint outFlags, ref IntPtr ppList);

    public static string AuthName(uint a) => a switch
    {
        1 => "OPEN", 2 => "WPA-Personal", 3 => "WPA-Enterprise",
        4 => "WPA-Personal+Enterprise", 5 => "WPA2-Personal", 6 => "WPA2-Enterprise",
        7 => "WPA2-Personal+Enterprise", 8 => "WPA3-Personal(SAE)",
        9 => "WPA3-Personal-192", 10 => "WPA3-Enterprise", 11 => "WPA3-Enterprise-192",
        12 => "WPA3-Personal-Transition", 13 => "WPA3-Enterprise-Transition",
        14 => "WPA3-Personal-192-Transition", 15 => "WPA3-Enterprise-192-Transition",
        _ => "auth#" + a
    };

    public static string PrivName(uint p) => p switch
    {
        1 => "NONE", 2 => "AES(CCMP)", 3 => "TKIP", 4 => "AES+TKIP", _ => "priv#" + p
    };
}
'@
Add-Type -TypeDefinition $code

$h = [IntPtr]::Zero; $nv = 0
$r = [Wnet]::Open(1, [IntPtr]::Zero, [ref]$nv, [ref]$h)
if ($r -ne 0) { throw "open failed 0x{0:X}" -f $r }
try {
    $lp = [IntPtr]::Zero
    $r = [Wnet]::Enum($h, [IntPtr]::Zero, [ref]$lp)
    if ($r -ne 0) { throw "enum failed 0x{0:X}" -f $r }
    $n = [uint32][System.Runtime.InteropServices.Marshal]::ReadInt32($lp)
    [Wnet]::Free($lp)

    $count = 0; $outFlags = 0; $ap = [IntPtr]::Zero
    $r = [Wnet]::Avail($h, 0, 0, [IntPtr]::Zero, [ref]$count, [ref]$outFlags, [ref]$ap)
    if ($r -ne 0) { throw "avail failed 0x{0:X}" -f $r }
    try {
        $cnt = [uint32][System.Runtime.InteropServices.Marshal]::ReadInt32($ap)
        Write-Host ("available networks: {0}" -f $cnt)
        $base = [long]$ap + 8
        for ($i = 0; $i -lt [int]$cnt; $i++) {
            $item = $base + [long]$i * 64
            $ssidPtr = [System.Runtime.InteropServices.Marshal]::ReadIntPtr([IntPtr]$item + 8)
            $secPtr  = [System.Runtime.InteropServices.Marshal]::ReadIntPtr([IntPtr]$item + 40)
            $conn    = [bool][System.Runtime.InteropServices.Marshal]::ReadInt32([IntPtr]$item + 56)
            $code    = [uint32][System.Runtime.InteropServices.Marshal]::ReadInt32([IntPtr]$item + 60)
            $ssid = '(hidden)'
            if ($ssidPtr -ne [IntPtr]::Zero) {
                $len = [uint32][System.Runtime.InteropServices.Marshal]::ReadInt32($ssidPtr)
                $raw = New-Object byte[] ($len * 2)
                [System.Runtime.InteropServices.Marshal]::Copy($ssidPtr + 4, $raw, 0, $len * 2)
                $ssid = [Text.Encoding]::Unicode.GetString($raw)
            }
            $sec = 'n/a'
            if ($secPtr -ne [IntPtr]::Zero) {
                $auth = [uint32][System.Runtime.InteropServices.Marshal]::ReadInt32($secPtr)
                $priv = [uint32][System.Runtime.InteropServices.Marshal]::ReadInt32([IntPtr]([long]$secPtr + 4))
                $sec = "{0}/{1}" -f [Wnet]::AuthName($auth), [Wnet]::PrivName($priv)
            }
            Write-Host ("{0,-24} auth={1,-28} connectable={2} code={3}" -f $ssid, $sec, $conn, $code)
        }
    } finally {
        [Wnet]::Free($ap)
    }
} finally {
    [Wnet]::Close($h, [IntPtr]::Zero)
}
