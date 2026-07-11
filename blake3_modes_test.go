package blake3

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// keyArray returns the official 32-byte key as a fixed array for NewKeyed.
func keyArray(t *testing.T) [keyLen]byte {
	t.Helper()
	if len(officialKey) != keyLen {
		t.Fatalf("official key is %d bytes, want %d", len(officialKey), keyLen)
	}
	var k [keyLen]byte
	copy(k[:], officialKey)
	return k
}

// TestKeyedVectors checks NewKeyed against the official BLAKE3 keyed_hash
// vectors, byte-for-byte, across every published input length (0..102400).
func TestKeyedVectors(t *testing.T) {
	key := keyArray(t)
	for _, v := range modeVectors {
		want, err := hex.DecodeString(v.keyed)
		if err != nil {
			t.Fatalf("n=%d: bad vector hex: %v", v.n, err)
		}
		in := patternInput(v.n)

		got := make([]byte, len(want))
		h := NewKeyed(key)
		h.Write(in)
		h.Digest(got)
		if !bytes.Equal(got, want) {
			t.Fatalf("keyed n=%d: mismatch\n got %x\nwant %x", v.n, got, want)
		}
		// The 32-byte Sum is the leading 32 bytes of the extendable output.
		if s := h.Sum(nil); !bytes.Equal(s, want[:32]) {
			t.Fatalf("keyed n=%d: Sum mismatch", v.n)
		}
	}
}

// TestDeriveKeyVectors checks NewDeriveKey against the official BLAKE3
// derive_key vectors, byte-for-byte, across every published input length.
func TestDeriveKeyVectors(t *testing.T) {
	for _, v := range modeVectors {
		want, err := hex.DecodeString(v.deriveKey)
		if err != nil {
			t.Fatalf("n=%d: bad vector hex: %v", v.n, err)
		}
		in := patternInput(v.n)

		got := make([]byte, len(want))
		h := NewDeriveKey(officialContext)
		h.Write(in)
		h.Digest(got)
		if !bytes.Equal(got, want) {
			t.Fatalf("derive_key n=%d: mismatch\n got %x\nwant %x", v.n, got, want)
		}
		if s := h.Sum(nil); !bytes.Equal(s, want[:32]) {
			t.Fatalf("derive_key n=%d: Sum mismatch", v.n)
		}
	}
}

// TestKeyedStreamingMatchesOneShot feeds a multi-chunk input in varying write
// chunk sizes and confirms the keyed streaming digest is stable and matches the
// single-write result (exercises the tree-merge path under KEYED_HASH flags).
func TestKeyedStreamingMatchesOneShot(t *testing.T) {
	key := keyArray(t)
	in := patternInput(8192 + 123)

	oneShot := NewKeyed(key)
	oneShot.Write(in)
	want := oneShot.Sum(nil)

	for _, step := range []int{1, 7, 64, 100, 1000, 1024, 4096} {
		h := NewKeyed(key)
		for off := 0; off < len(in); off += step {
			end := off + step
			if end > len(in) {
				end = len(in)
			}
			if n, err := h.Write(in[off:end]); err != nil || n != end-off {
				t.Fatalf("step=%d: Write = (%d,%v)", step, n, err)
			}
		}
		if got := h.Sum(nil); !bytes.Equal(got, want) {
			t.Fatalf("keyed step=%d: streaming digest mismatch", step)
		}
	}
}

// TestDeriveKeyStreamingMatchesOneShot mirrors the keyed streaming check for
// derive-key mode.
func TestDeriveKeyStreamingMatchesOneShot(t *testing.T) {
	in := patternInput(4096 + 57)

	oneShot := NewDeriveKey(officialContext)
	oneShot.Write(in)
	want := oneShot.Sum(nil)

	for _, step := range []int{1, 63, 1024, 2048} {
		h := NewDeriveKey(officialContext)
		for off := 0; off < len(in); off += step {
			end := off + step
			if end > len(in) {
				end = len(in)
			}
			h.Write(in[off:end])
		}
		if got := h.Sum(nil); !bytes.Equal(got, want) {
			t.Fatalf("derive_key step=%d: streaming digest mismatch", step)
		}
	}
}

// TestModesAreDistinct confirms the three modes domain-separate: the same input
// under unkeyed / keyed / derive-key must yield three different digests.
func TestModesAreDistinct(t *testing.T) {
	key := keyArray(t)
	in := patternInput(200)

	unkeyed := Sum256(in)

	kh := NewKeyed(key)
	kh.Write(in)
	keyed := kh.Sum(nil)

	dh := NewDeriveKey(officialContext)
	dh.Write(in)
	derived := dh.Sum(nil)

	if bytes.Equal(unkeyed[:], keyed) {
		t.Fatal("keyed digest equals unkeyed digest")
	}
	if bytes.Equal(unkeyed[:], derived) {
		t.Fatal("derive-key digest equals unkeyed digest")
	}
	if bytes.Equal(keyed, derived) {
		t.Fatal("keyed digest equals derive-key digest")
	}
}

// TestKeyedReset confirms Reset preserves the keyed mode (key + flags).
func TestKeyedReset(t *testing.T) {
	key := keyArray(t)
	h := NewKeyed(key)
	h.Write(patternInput(5000))
	h.Reset()
	h.Write([]byte("abc"))
	got := h.Sum(nil)

	ref := NewKeyed(key)
	ref.Write([]byte("abc"))
	want := ref.Sum(nil)
	if !bytes.Equal(got, want) {
		t.Fatal("Reset did not restore a clean keyed state")
	}
}

// TestDeriveKeyReset confirms Reset preserves the derive-key mode.
func TestDeriveKeyReset(t *testing.T) {
	h := NewDeriveKey(officialContext)
	h.Write(patternInput(3000))
	h.Reset()
	h.Write([]byte("abc"))
	got := h.Sum(nil)

	ref := NewDeriveKey(officialContext)
	ref.Write([]byte("abc"))
	want := ref.Sum(nil)
	if !bytes.Equal(got, want) {
		t.Fatal("Reset did not restore a clean derive-key state")
	}
}
