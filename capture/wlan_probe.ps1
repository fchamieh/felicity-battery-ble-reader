Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public sealed class W3 {
  [DllImport("wlanapi.dll", EntryPoint="WlanOpenHandle")]
  public static extern uint OpenH(uint v, IntPtr r, ref uint nv, out IntPtr h);
  [DllImport("wlanapi.dll", EntryPoint="WlanLowPrivOpenHandle")]
  public static extern uint LowPrivOpen4(uint v, IntPtr r, ref uint nv, out IntPtr h);
  [DllImport("wlanapi.dll", EntryPoint="WlanCloseHandle")]
  public static extern void Close(IntPtr h, IntPtr r);
  [DllImport("wlanapi.dll", EntryPoint="WlanEnumInterfaces")]
  public static extern uint Enum(IntPtr h, IntPtr r, ref List l);
  [StructLayout(LayoutKind.Sequential)]
  public struct List { public uint n; public IntPtr p; }
}
'@
foreach ($f in @('OpenH','LowPrivOpen4')) {
  foreach ($v in @(1,2)) {
    $h=[IntPtr]::Zero; $nv=0
    try {
      $rc = [W3]::$f($v,[IntPtr]::Zero,[ref]$nv,[ref]$h)
      Write-Host ($f + " v=" + $v + " -> 0x" + $rc.ToString('X') + " neg=" + $nv)
      if ($rc -eq 0) {
        $l = New-Object W3+List
        $rc2 = [W3]::Enum($h,[IntPtr]::Zero,[ref]$l)
        Write-Host ("   enum -> 0x" + $rc2.ToString('X') + " count=" + $l.n)
        [W3]::Close($h,[IntPtr]::Zero)
      }
    } catch [System.ComponentModel.Win32Exception] {
      Write-Host ($f + " v=" + $v + " -> win32 0x" + $_.Exception.HResult.ToString('X'))
    } catch {
      Write-Host ($f + " v=" + $v + " -> " + $_.Exception.Message)
    }
  }
}
