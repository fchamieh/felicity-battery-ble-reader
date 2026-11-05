$ErrorActionPreference = 'Stop'
$code = @'
using System;
using System.Runtime.InteropServices;
public sealed class Wnet2
{
    [DllImport("wlanapi.dll", EntryPoint = "WlanOpenHandle", SetLastError = true)]
    public static extern uint Open(uint v, IntPtr r, ref uint nv, out IntPtr h);
    [DllImport("wlanapi.dll", EntryPoint = "WlanCloseHandle")]
    public static extern void Close(IntPtr h, IntPtr r);
    [DllImport("wlanapi.dll", EntryPoint = "WlanFreeMemory")]
    public static extern void Free(IntPtr p);
    [DllImport("wlanapi.dll", EntryPoint = "WlanGetAvailableNetworkList", SetLastError = true)]
    public static extern uint AvailA(IntPtr h, uint reserved, uint flags,
        IntPtr pInterfaceGuid, ref uint count, ref uint outFlags, ref IntPtr ppList);
    [DllImport("wlanapi.dll", EntryPoint = "WlanGetAvailableNetworkList2", SetLastError = true)]
    public static extern uint AvailB(IntPtr h, uint reserved, uint flags,
        IntPtr pInterfaceGuid, ref uint count, ref uint outFlags, ref IntPtr ppList);
    [DllImport("wlanapi.dll", EntryPoint = "WlanGetAvailableNetworkList2", SetLastError = true)]
    public static extern uint AvailC(IntPtr h, uint flags,
        ref Guid pInterfaceGuid, ref uint count, ref uint outFlags, ref IntPtr ppList);
}
'@
Add-Type -TypeDefinition $code
$h = [IntPtr]::Zero; $nv = 0
[Wnet2]::Open(1, [IntPtr]::Zero, [ref]$nv, [ref]$h) | Out-Null
$guid = [Guid]'2876383c-7bdf-4f89-95f3-e4e9b229a930'
$gp = [IntPtr]::Zero; $gpHandle = [Runtime.InteropServices.GCHandle]::Alloc($guid, [Runtime.InteropServices.GCHandleType]::Pinned)
$gp = $gpHandle.AddrOfPinnedObject()
try {
    foreach ($combo in @('A-null','A-g','B-null','B-g','C-g')) {
        $count = 0; $of = 0; $ap = [IntPtr]::Zero
        $r = 999
        switch ($combo) {
            'A-null' { $r = [Wnet2]::AvailA($h, 0, 0, [IntPtr]::Zero, [ref]$count, [ref]$of, [ref]$ap) }
            'A-g'    { $r = [Wnet2]::AvailA($h, 0, 0, $gp, [ref]$count, [ref]$of, [ref]$ap) }
            'B-null' { $r = [Wnet2]::AvailB($h, 0, 0, [IntPtr]::Zero, [ref]$count, [ref]$of, [ref]$ap) }
            'B-g'    { $r = [Wnet2]::AvailB($h, 0, 0, $gp, [ref]$count, [ref]$of, [ref]$ap) }
            'C-g'    { $r = [Wnet2]::AvailC($h, 0, [ref]$guid, [ref]$count, [ref]$of, [ref]$ap) }
        }
        $desc = "rc=0x{0:X}" -f $r
        if ($r -eq 0 -and $ap -ne [IntPtr]::Zero) {
            $cnt = [uint32][System.Runtime.InteropServices.Marshal]::ReadInt32($ap)
            $desc += " count=" + $cnt
            [Wnet2]::Free($ap)
        }
        Write-Host ($combo + " -> " + $desc)
    }
} finally {
    [Runtime.InteropServices.GCHandle]::Free($gpHandle)
    [Wnet2]::Close($h, [IntPtr]::Zero)
}
