package blake3

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// officialVectors are taken from the BLAKE3 reference test-vector input pattern
// (input[i] = i % 251), default (unkeyed) hash mode, 131 bytes of extendable
// output. The length-0 entry is the canonical published empty-input hash, which
// anchors the rest.
var officialVectors = []struct {
	n   int
	hex string
}{
	{0, "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262e00f03e7b69af26b7faaf09fcd333050338ddfe085b8cc869ca98b206c08243a26f5487789e8f660afe6c99ef9e0c52b92e7393024a80459cf91f476f9ffdbda7001c22e159b402631f277ca96f2defdf1078282314e763699a31c5363165421cce14d"},
	{1, "2d3adedff11b61f14c886e35afa036736dcd87a74d27b5c1510225d0f592e213c3a6cb8bf623e20cdb535f8d1a5ffb86342d9c0b64aca3bce1d31f60adfa137b358ad4d79f97b47c3d5e79f179df87a3b9776ef8325f8329886ba42f07fb138bb502f4081cbcec3195c5871e6c23e2cc97d3c69a613eba131e5f1351f3f1da786545e5"},
	{2, "7b7015bb92cf0b318037702a6cdd81dee41224f734684c2c122cd6359cb1ee63d8386b22e2ddc05836b7c1bb693d92af006deb5ffbc4c70fb44d0195d0c6f252faac61659ef86523aa16517f87cb5f1340e723756ab65efb2f91964e14391de2a432263a6faf1d146937b35a33621c12d00be8223a7f1919cec0acd12097ff3ab00ab1"},
	{3, "e1be4d7a8ab5560aa4199eea339849ba8e293d55ca0a81006726d184519e647f5b49b82f805a538c68915c1ae8035c900fd1d4b13902920fd05e1450822f36de9454b7e9996de4900c8e723512883f93f4345f8a58bfe64ee38d3ad71ab027765d25cdd0e448328a8e7a683b9a6af8b0af94fa09010d9186890b096a08471e4230a134"},
	{63, "e9bc37a594daad83be9470df7f7b3798297c3d834ce80ba85d6e207627b7db7b1197012b1e7d9af4d7cb7bdd1f3bb49a90a9b5dec3ea2bbc6eaebce77f4e470cbf4687093b5352f04e4a4570fba233164e6acc36900e35d185886a827f7ea9bdc1e5c3ce88b095a200e62c10c043b3e9bc6cb9b6ac4dfa51794b02ace9f98779040755"},
	{64, "4eed7141ea4a5cd4b788606bd23f46e212af9cacebacdc7d1f4c6dc7f2511b98fc9cc56cb831ffe33ea8e7e1d1df09b26efd2767670066aa82d023b1dfe8ab1b2b7fbb5b97592d46ffe3e05a6a9b592e2949c74160e4674301bc3f97e04903f8c6cf95b863174c33228924cdef7ae47559b10b294acd660666c4538833582b43f82d74"},
	{65, "de1e5fa0be70df6d2be8fffd0e99ceaa8eb6e8c93a63f2d8d1c30ecb6b263dee0e16e0a4749d6811dd1d6d1265c29729b1b75a9ac346cf93f0e1d7296dfcfd4313b3a227faaaaf7757cc95b4e87a49be3b8a270a12020233509b1c3632b3485eef309d0abc4a4a696c9decc6e90454b53b000f456a3f10079072baaf7a981653221f2c"},
	{127, "d81293fda863f008c09e92fc382a81f5a0b4a1251cba1634016a0f86a6bd640de3137d477156d1fde56b0cf36f8ef18b44b2d79897bece12227539ac9ae0a5119da47644d934d26e74dc316145dcb8bb69ac3f2e05c242dd6ee06484fcb0e956dc44355b452c5e2bbb5e2b66e99f5dd443d0cbcaaafd4beebaed24ae2f8bb672bcef78"},
	{128, "f17e570564b26578c33bb7f44643f539624b05df1a76c81f30acd548c44b45efa69faba091427f9c5c4caa873aa07828651f19c55bad85c47d1368b11c6fd99e47ecba5820a0325984d74fe3e4058494ca12e3f1d3293d0010a9722f7dee64f71246f75e9361f44cc8e214a100650db1313ff76a9f93ec6e84edb7add1cb4a95019b0c"},
	{129, "683aaae9f3c5ba37eaaf072aed0f9e30bac0865137bae68b1fde4ca2aebdcb12f96ffa7b36dd78ba321be7e842d364a62a42e3746681c8bace18a4a8a79649285c7127bf8febf125be9de39586d251f0d41da20980b70d35e3dac0eee59e468a894fa7e6a07129aaad09855f6ad4801512a116ba2b7841e6cfc99ad77594a8f2d181a7"},
	{1023, "10108970eeda3eb932baac1428c7a2163b0e924c9a9e25b35bba72b28f70bd11a182d27a591b05592b15607500e1e8dd56bc6c7fc063715b7a1d737df5bad3339c56778957d870eb9717b57ea3d9fb68d1b55127bba6a906a4a24bbd5acb2d123a37b28f9e9a81bbaae360d58f85e5fc9d75f7c370a0cc09b6522d9c8d822f2f28f485"},
	{1024, "42214739f095a406f3fc83deb889744ac00df831c10daa55189b5d121c855af71cf8107265ecdaf8505b95d8fcec83a98a6a96ea5109d2c179c47a387ffbb404756f6eeae7883b446b70ebb144527c2075ab8ab204c0086bb22b7c93d465efc57f8d917f0b385c6df265e77003b85102967486ed57db5c5ca170ba441427ed9afa684e"},
	{1025, "d00278ae47eb27b34faecf67b4fe263f82d5412916c1ffd97c8cb7fb814b8444f4c4a22b4b399155358a994e52bf255de60035742ec71bd08ac275a1b51cc6bfe332b0ef84b409108cda080e6269ed4b3e2c3f7d722aa4cdc98d16deb554e5627be8f955c98e1d5f9565a9194cad0c4285f93700062d9595adb992ae68ff12800ab67a"},
	{2048, "e776b6028c7cd22a4d0ba182a8bf62205d2ef576467e838ed6f2529b85fba24a9a60bf80001410ec9eea6698cd537939fad4749edd484cb541aced55cd9bf54764d063f23f6f1e32e12958ba5cfeb1bf618ad094266d4fc3c968c2088f677454c288c67ba0dba337b9d91c7e1ba586dc9a5bc2d5e90c14f53a8863ac75655461cea8f9"},
	{2049, "5f4d72f40d7a5f82b15ca2b2e44b1de3c2ef86c426c95c1af0b687952256303096de31d71d74103403822a2e0bc1eb193e7aecc9643a76b7bbc0c9f9c52e8783aae98764ca468962b5c2ec92f0c74eb5448d519713e09413719431c802f948dd5d90425a4ecdadece9eb178d80f26efccae630734dff63340285adec2aed3b51073ad3"},
	{3072, "b98cb0ff3623be03326b373de6b9095218513e64f1ee2edd2525c7ad1e5cffd29a3f6b0b978d6608335c09dc94ccf682f9951cdfc501bfe47b9c9189a6fc7b404d120258506341a6d802857322fbd20d3e5dae05b95c88793fa83db1cb08e7d8008d1599b6209d78336e24839724c191b2a52a80448306e0daa84a3fdb566661a37e11"},
	{3073, "7124b49501012f81cc7f11ca069ec9226cecb8a2c850cfe644e327d22d3e1cd39a27ae3b79d68d89da9bf25bc27139ae65a324918a5f9b7828181e52cf373c84f35b639b7fccbb985b6f2fa56aea0c18f531203497b8bbd3a07ceb5926f1cab74d14bd66486d9a91eba99059a98bd1cd25876b2af5a76c3e9eed554ed72ea952b603bf"},
	{4096, "015094013f57a5277b59d8475c0501042c0b642e531b0a1c8f58d2163229e9690289e9409ddb1b99768eafe1623da896faf7e1114bebeadc1be30829b6f8af707d85c298f4f0ff4d9438aef948335612ae921e76d411c3a9111df62d27eaf871959ae0062b5492a0feb98ef3ed4af277f5395172dbe5c311918ea0074ce0036454f620"},
	{5120, "9cadc15fed8b5d854562b26a9536d9707cadeda9b143978f319ab34230535833acc61c8fdc114a2010ce8038c853e121e1544985133fccdd0a2d507e8e615e611e9a0ba4f47915f49e53d721816a9198e8b30f12d20ec3689989175f1bf7a300eee0d9321fad8da232ece6efb8e9fd81b42ad161f6b9550a069e66b11b40487a5f5059"},
	{6144, "3e2e5b74e048f3add6d21faab3f83aa44d3b2278afb83b80b3c35164ebeca2054d742022da6fdda444ebc384b04a54c3ac5839b49da7d39f6d8a9db03deab32aade156c1c0311e9b3435cde0ddba0dce7b26a376cad121294b689193508dd63151603c6ddb866ad16c2ee41585d1633a2cea093bea714f4c5d6b903522045b20395c83"},
	{7168, "61da957ec2499a95d6b8023e2b0e604ec7f6b50e80a9678b89d2628e99ada77a5707c321c83361793b9af62a40f43b523df1c8633cecb4cd14d00bdc79c78fca5165b863893f6d38b02ff7236c5a9a8ad2dba87d24c547cab046c29fc5bc1ed142e1de4763613bb162a5a538e6ef05ed05199d751f9eb58d332791b8d73fb74e4fce95"},
	{8192, "aae792484c8efe4f19e2ca7d371d8c467ffb10748d8a5a1ae579948f718a2a635fe51a27db045a567c1ad51be5aa34c01c6651c4d9b5b5ac5d0fd58cf18dd61a47778566b797a8c67df7b1d60b97b19288d2d877bb2df417ace009dcb0241ca1257d62712b6a4043b4ff33f690d849da91ea3bf711ed583cb7b7a7da2839ba71309bbf"},
}

func patternInput(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func TestOfficialVectors(t *testing.T) {
	for _, v := range officialVectors {
		want, _ := hex.DecodeString(v.hex)
		in := patternInput(v.n)

		// Extendable (XOF) output of the full 131 bytes.
		got := make([]byte, len(want))
		h := New()
		h.Write(in)
		h.Digest(got)
		if !bytes.Equal(got, want) {
			t.Fatalf("n=%d: Digest mismatch\n got %x\nwant %x", v.n, got, want)
		}
		// Sum256 / Sum512 are the leading 32 / 64 bytes of that output.
		if s := Sum256(in); !bytes.Equal(s[:], want[:32]) {
			t.Fatalf("n=%d: Sum256 mismatch", v.n)
		}
		if s := Sum512(in); !bytes.Equal(s[:], want[:64]) {
			t.Fatalf("n=%d: Sum512 mismatch", v.n)
		}
	}
}

// TestParallelMatchesStreaming cross-checks the parallel one-shot path
// (Sum256/Sum512, used for inputs above the parallel threshold) against the
// sequential streaming path (verified against the official vectors), for inputs
// large enough to trigger multi-core chunk hashing.
func TestParallelMatchesStreaming(t *testing.T) {
	for _, n := range []int{16*1024 + 1, 100 * 1024, 1 << 20} {
		in := patternInput(n)
		// Streaming (sequential) reference for the same input.
		h := New()
		h.Write(in)
		var want [64]byte
		h.Digest(want[:])

		if got := Sum256(in); !bytes.Equal(got[:], want[:32]) {
			t.Fatalf("n=%d: parallel Sum256 != streaming", n)
		}
		if got := Sum512(in); !bytes.Equal(got[:], want[:]) {
			t.Fatalf("n=%d: parallel Sum512 != streaming", n)
		}
	}
}

func TestStreamingMatchesOneShot(t *testing.T) {
	in := patternInput(8192 + 123) // spans many chunks + a partial tail
	want := Sum256(in)
	for _, step := range []int{1, 7, 64, 100, 1000, 1024, 4096} {
		h := New()
		for off := 0; off < len(in); off += step {
			end := off + step
			if end > len(in) {
				end = len(in)
			}
			if n, err := h.Write(in[off:end]); err != nil || n != end-off {
				t.Fatalf("step=%d: Write = (%d,%v)", step, n, err)
			}
		}
		if got := h.Sum(nil); !bytes.Equal(got, want[:]) {
			t.Fatalf("step=%d: streaming digest mismatch", step)
		}
	}
}

func TestSumIsNonDestructive(t *testing.T) {
	h := New()
	h.Write([]byte("hello "))
	first := h.Sum(nil)
	second := h.Sum(nil)
	if !bytes.Equal(first, second) {
		t.Fatal("Sum mutated state (two calls differ)")
	}
	// Writing more after Sum must continue the same hash.
	h.Write([]byte("world"))
	got := h.Sum(nil)
	want := Sum256([]byte("hello world"))
	if !bytes.Equal(got, want[:]) {
		t.Fatal("Write after Sum produced the wrong digest")
	}
	// Sum appends to its argument.
	prefix := []byte{0xAA, 0xBB}
	out := h.Sum(prefix)
	if !bytes.Equal(out[:2], prefix) || len(out) != 2+32 {
		t.Fatalf("Sum did not append to b: len=%d", len(out))
	}
}

func TestReset(t *testing.T) {
	h := New()
	h.Write(patternInput(5000))
	h.Reset()
	h.Write([]byte("abc"))
	got := h.Sum(nil)
	want := Sum256([]byte("abc"))
	if !bytes.Equal(got, want[:]) {
		t.Fatal("Reset did not restore a clean state")
	}
}

func TestSizes(t *testing.T) {
	h := New()
	if h.Size() != 32 || h.BlockSize() != 64 {
		t.Fatalf("Size=%d BlockSize=%d", h.Size(), h.BlockSize())
	}
}
