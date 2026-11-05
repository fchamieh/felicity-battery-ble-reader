import javax.crypto.*; import javax.crypto.spec.*; import java.util.*;
public class AESGround {
  public static void main(String[] a) throws Exception {
    SecretKey k = new SecretKeySpec("sxd_aiot_key_001".getBytes("US-ASCII"), "AES");
    Cipher c = Cipher.getInstance("AES/CBC/NoPadding");
    c.init(Cipher.ENCRYPT_MODE, k, new IvParameterSpec("sxd_aiot_2022_01".getBytes("US-ASCII")));
    byte[] out = c.doFinal(new byte[16]);
    StringBuilder sb = new StringBuilder();
    for (byte b : out) sb.append(String.format("%02X", b));
    System.out.println("encrypt(16 zero bytes) = " + sb);
    System.out.println("blocksize=" + c.getBlockSize() + " mode=" + c.getParameters());
  }
}
