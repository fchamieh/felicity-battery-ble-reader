import java.nio.charset.StandardCharsets;
import javax.crypto.Cipher;
import javax.crypto.spec.IvParameterSpec;
import javax.crypto.spec.SecretKeySpec;

// Replicates FSolar's binary frame builder byte-for-byte:
//   b.b.a()      CRC                (b/b.java)
//   b.a.b/c/d    u16be, concat, AES (b/a.java)
//   c.c.a()      frame assembly     (c/c.java line 62, c/b.java, c/a.java)
public class FrameGround {
    // Filled from the standard CRC-16/MODBUS table; CRCGround.java already
    // proved these equal the app's hard-coded f6356a/f6357b arrays.
    static final byte[] A = new byte[256]; // f6356a
    static final byte[] B = new byte[256]; // f6357b

    static {
        for (int i = 0; i < 256; i++) {
            int c = i;
            for (int k = 0; k < 8; k++) {
                c = ((c & 1) != 0) ? (c >> 1) ^ 0xA001 : c >> 1;
            }
            B[i] = (byte) (c >> 8);
            A[i] = (byte) (c & 255);
        }
    }

    static int crc(byte[] d) {
        int lo = 255;
        byte hi = (byte) 255;
        for (byte x : d) {
            int idx = (lo ^ x) & 255;
            int nlo = hi ^ A[idx];
            byte nhi = B[idx];
            lo = nlo;
            hi = nhi;
        }
        return ((hi & 255) << 8) | (lo & 255);
    }

    static byte[] u16(int v) { return new byte[]{(byte) (v >> 8), (byte) v}; }

    static byte[] cat(byte[]... parts) {
        int n = 0;
        for (byte[] p : parts) n += p.length;
        byte[] out = new byte[n];
        int off = 0;
        for (byte[] p : parts) { System.arraycopy(p, 0, out, off, p.length); off += p.length; }
        return out;
    }

    static byte[] enc(byte[] plain) throws Exception {
        SecretKeySpec k = new SecretKeySpec("sxd_aiot_key_001".getBytes(StandardCharsets.UTF_8), "AES");
        Cipher c = Cipher.getInstance("AES/CBC/NoPadding");
        c.init(Cipher.ENCRYPT_MODE, k, new IvParameterSpec("sxd_aiot_2022_01".getBytes(StandardCharsets.UTF_8)));
        return c.doFinal(plain);
    }

    static final byte[] MARKER = "0000000000".getBytes(StandardCharsets.UTF_8);
    static final byte[] CONST = {0, 6};

    // c/b.java d(int[]): command 25 == 0x19
    static byte[] readReq(int[] ids) throws Exception {
        byte[] body = new byte[ids.length * 2];
        for (int i = 0; i < ids.length; i++) {
            body[i * 2] = u16(ids[i])[0];
            body[i * 2 + 1] = u16(ids[i])[1];
        }
        int m = (body.length + 12) % 16;
        byte[] pad = (m == 0) ? new byte[0] : new byte[16 - m];
        int encLen = body.length + 14;
        byte[] plain = cat(MARKER, u16(ids.length), body, pad);
        byte[] frame = cat(u16(encLen + 4 + pad.length + 2), CONST, u16(encLen), new byte[]{1}, new byte[]{25}, enc(plain));
        return cat(frame, u16(crc(frame)));
    }

    // c/a.java d(List<Param>): command 24 == 0x18, param = no(2) + len(2) + value
    static byte[] writeReq(int[] nos, byte[][] vals) throws Exception {
        byte[][] ps = new byte[nos.length][];
        int bodyLen = 0;
        for (int i = 0; i < nos.length; i++) {
            ps[i] = cat(u16(nos[i]), u16(vals[i].length), vals[i]);
            bodyLen += ps[i].length;
        }
        byte[] body = new byte[bodyLen];
        int off = 0;
        for (byte[] p : ps) { System.arraycopy(p, 0, body, off, p.length); off += p.length; }
        int m = (body.length + 14) % 16;
        byte[] pad = (m == 0) ? new byte[0] : new byte[16 - m];
        int encLen = body.length + 16;
        byte[] plain = cat(MARKER, u16(nos.length), u16(body.length), body, pad);
        byte[] frame = cat(u16(encLen + 4 + pad.length + 2), CONST, u16(encLen), new byte[]{1}, new byte[]{24}, enc(plain));
        return cat(frame, u16(crc(frame)));
    }

    static String hex(byte[] b) {
        StringBuilder s = new StringBuilder();
        for (byte x : b) s.append(String.format("%02X", x));
        return s.toString();
    }

    public static void main(String[] args) throws Exception {
        System.out.println("crc(123456789) = " + String.format("%04X", crc("123456789".getBytes(StandardCharsets.US_ASCII))));
        System.out.println("read1    = " + hex(readReq(new int[]{5})));
        System.out.println("read3    = " + hex(readReq(new int[]{1, 2, 3})));
        System.out.println("write2   = " + hex(writeReq(new int[]{1, 7}, new byte[][]{{0x01, 0x02}, {0x0A}})));
    }
}
