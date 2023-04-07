package main_test

import (
	"fmt"
	"math/bits"
	"math/rand"
	"reflect"
	"testing"
)

//go:generate go run . -package main_test gen_test.go

// TestIncrementDelta verifies that an incrementing counter fits the single-bit
// pack.
func TestIncrementDelta(t *testing.T) {
	// test input with each value 1 more than the previous
	offset := int32(-10)
	var input [64]int32
	for i := range input {
		input[i] = offset + int32(i+1)
	}

	pack := append1BitDeltaEncode(nil, &input, offset)
	if len(pack) != 1 || pack[0] != 0xffff_ffff_ffff_ffff {
		t.Errorf("packed as %#x, want [0xffffffffffffffff]", pack)
	}

	got := append1BitDeltaDecode(nil, (*[1]uint64)(pack), offset)
	for i := range got {
		if got[i] != input[i] {
			t.Errorf("encode + decode changed input word[%d]: got %#x, want %#x", i, got[i], input[i])
		}
	}
}

// TestDecrementDelta verifies that a decrementing counter fits the double-bit
// pack.
func TestDecrementDelta(t *testing.T) {
	// test input with each value 1 less than the previous
	offset := int64(10)
	var input [64]int64
	for i := range input {
		input[i] = offset - int64(i+1)
	}

	pack := append2BitDeltaEncode(nil, &input, offset)
	if len(pack) != 2 || pack[0] != 0xaaaa_aaaa_aaaa_aaaa || pack[1] != 0xaaaa_aaaa_aaaa_aaaa {
		t.Errorf("packed as %#x, want [0xaaaaaaaaaaaaaaaa 0xaaaaaaaaaaaaaaaa]", pack)
	}

	got := append2BitDeltaDecode(nil, (*[2]uint64)(pack), offset)
	for i := range got {
		if got[i] != input[i] {
			t.Errorf("encode + decode changed input word[%d]: got %#x, want %#x", i, got[i], input[i])
		}
	}
}

// TestDeltaEncoding tests encode & decode for each supported bit-size.
func TestDeltaEncoding(t *testing.T) {
	for bitN := 0; bitN <= 64; bitN++ {
		t.Run(fmt.Sprintf("%dBitDelta", bitN), func(t *testing.T) {
			if bitN <= 32 {
				t.Run("int32", func(t *testing.T) {
					testDeltaEncoding[int32](t, bitN)
				})
			}
			t.Run("int64", func(t *testing.T) {
				testDeltaEncoding[int64](t, bitN)
			})
			t.Run("uint64", func(t *testing.T) {
				testDeltaEncoding[uint64](t, bitN)
			})
		})
	}
}

func testDeltaEncoding[T int | int32 | int64 | uint64](t *testing.T, bitN int) {
	data, offset := randomNBitDeltas[T](t, bitN)

	in := data // copy just in case encode mutates input
	pack := AppendDeltaEncode(nil, &in, offset)

	expectN := bitN
	if expectN > 42 {
		expectN = 64
	}
	if len(pack) != expectN {
		t.Errorf("packed %d-bit random data in %d words, want %d", bitN, len(pack), expectN)
	}

	got := AppendDeltaDecode(nil, pack, offset)
	want := data[:]
	if !reflect.DeepEqual(got, want) {
		t.Logf("packed as: %#x", pack)
		t.Errorf("encode + decode changed input\ngot:  %#x\nwant: %#x", got, want)
	}
}

func BenchmarkDeltaBitEncoding(b *testing.B) {
	for _, bitN := range []int{1, 7, 32, 63} {
		b.Run(fmt.Sprintf("%dBitDelta", bitN), func(b *testing.B) {
			if bitN <= 32 {
				b.Run("int32", func(b *testing.B) {
					benchmarkDeltaBitEncoding[int32](b, bitN)
				})
			}
			b.Run("int64", func(b *testing.B) {
				benchmarkDeltaBitEncoding[int64](b, bitN)
			})
			b.Run("uint64", func(b *testing.B) {
				benchmarkDeltaBitEncoding[uint64](b, bitN)
			})
		})
	}
}

func benchmarkDeltaBitEncoding[T int | int32 | int64 | uint64](b *testing.B, bitN int) {
	data, offset := randomNBitDeltas[T](b, bitN)

	b.Run("Encode", func(b *testing.B) {
		b.SetBytes(int64(bits.OnesCount64(uint64(^T(0))) / 8))

		var dst []uint64 // bufer reused
		for i := 0; i < b.N; i += len(data) {
			dst = AppendDeltaEncode(dst[:0], &data, offset)
		}
	})

	b.Run("Decode", func(b *testing.B) {
		b.SetBytes(int64(bits.OnesCount64(uint64(^T(0))) / 8))

		src := AppendDeltaEncode(nil, &data, offset)

		var dst []T // buffer reused
		for i := 0; i < b.N; i += len(data) {
			dst = AppendDeltaDecode(dst[:0], src, offset)
		}
	})
}

// RandomNBitDelta generates a pseudo random data set with it's deltas zig-zag
// encoded less than or equal to bitN in size.
func randomNBitDeltas[T int | int32 | int64 | uint64](t testing.TB, bitN int) (data [64]T, offset T) {
	randomYetConsistent := rand.New(rand.NewSource(42))

	offset = T(randomYetConsistent.Uint64())

	switch bitN {
	case 0:
		// same value causes zero delta
		for i := range data {
			data[i] = offset
		}
		return

	case bits.OnesCount64(uint64(^T(0))):
		// no compression; delta equals word width
		for i := range data {
			data[i] = T(randomYetConsistent.Uint64())
		}
		return
	}

	// limit bit size of (zig-zag encoded) deltas
	mask := T(1)<<bitN - 1

	for i := range data {
		pass := offset
		if i > 0 {
			pass = data[i-1]
		}

		for {
			zigZagDelta := randomYetConsistent.Int63() & int64(mask)
			// decode
			delta := (zigZagDelta >> 1) ^ -(zigZagDelta & 1)
			// apply
			data[i] = pass - T(delta)
			// overflow check
			if (data[i] < pass) != (delta > 0) || (data[i] > pass) != (delta < 0) {
				t.Logf("retry on random delta %#x (zig-zag encodes as %#x) as it overflows %T offset %#x",
					delta, zigZagDelta, offset, offset)
				continue
			}
			break
		}
	}

	return
}
