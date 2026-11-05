#!/usr/bin/env python3
"""Dump all strings from an Android resources.arsc (string pools only)."""
import struct
import sys


def parse_string_pool(buf: bytes, off: int) -> list:
    typ, hsize, size, scount, ycount, flags = struct.unpack_from('<HHIIII', buf, off)
    if typ != 0x0001:
        return []
    stringsStart, _stylesStart = struct.unpack_from('<II', buf, off + 16)
    if stringsStart & 0x80000000:
        strings_off = off + (stringsStart & 0x7FFFFFFF)
    else:
        strings_off = stringsStart
    out = []
    for i in range(scount):
        o = struct.unpack_from('<I', buf, off + 28 + 4 * i)[0]
        pos = strings_off + o
        try:
            if flags & 0x100:
                n = buf[pos]
                if n & 0x80:
                    pos += 2
                else:
                    pos += 1
                blen = buf[pos]
                if blen & 0x80:
                    pos += 2
                else:
                    pos += 1
                out.append(buf[pos:pos + blen].decode('utf-8', 'replace'))
            else:
                n = struct.unpack_from('<H', buf, pos)[0]
                out.append(buf[pos + 2:pos + 2 + 2 * n].decode('utf-16-le', 'replace'))
        except Exception:
            pass
    return out


def walk(buf: bytes, off: int, size: int) -> None:
    end = off + size
    pos = off
    while pos + 8 <= end:
        typ, hsize, csize = struct.unpack_from('<HHI', buf, pos)
        if csize < 8 or pos + csize > end or hsize < 8:
            break
        if typ == 0x0001:  # string pool
            for s in parse_string_pool(buf, pos):
                print(s)
        elif typ in (0x0002, 0x0200):  # table / package
            walk(buf, pos + hsize, csize - hsize)
        elif typ == 0x0201:  # type (may hold an inline string pool at the tail)
            _id, entry_count, flags, _start = struct.unpack_from('<IIII', buf, pos + 8)
            pool_off = pos + 8 + 16 + 20 + entry_count * 16
            if flags & 1 and pool_off + 8 <= pos + csize:
                t2, h2, s2 = struct.unpack_from('<HHI', buf, pool_off)
                if t2 == 0x0001 and s2 <= pos + csize - pool_off:
                    for s in parse_string_pool(buf, pool_off):
                        print(s)
        pos += csize


if __name__ == '__main__':
    sys.stdout.reconfigure(encoding='utf-8', errors='replace')
    with open(sys.argv[1], 'rb') as f:
        data = f.read()
    walk(data, 0, len(data))
