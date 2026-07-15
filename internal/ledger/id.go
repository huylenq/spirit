package ledger

import (
	"crypto/rand"
	"sync"
	"time"
)

// newULID returns a 26-character ULID (https://github.com/ulid/spec):
// 48-bit millisecond timestamp + 80 bits of randomness, Crockford base32.
// Lexicographic order equals time order, which is what the cursor relies on.
// Hand-rolled to keep the repo dependency-free; monotonicity within the same
// millisecond is ensured by bumping the random component.
func newULID(t time.Time) string {
	ulidMu.Lock()
	defer ulidMu.Unlock()

	ms := uint64(t.UnixMilli())
	var entropy [10]byte
	if ms == lastMS {
		// Same millisecond: increment the previous entropy so IDs stay
		// strictly increasing.
		entropy = lastEntropy
		for i := 9; i >= 0; i-- {
			entropy[i]++
			if entropy[i] != 0 {
				break
			}
		}
	} else if _, err := rand.Read(entropy[:]); err != nil {
		// Degenerate fallback: derive entropy from the clock; still unique
		// under the monotonic bump above.
		n := uint64(t.UnixNano())
		for i := 0; i < 8; i++ {
			entropy[i] = byte(n >> (8 * i))
		}
	}
	lastMS, lastEntropy = ms, entropy

	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	copy(b[6:], entropy[:])
	return encodeBase32(b)
}

var (
	ulidMu      sync.Mutex
	lastMS      uint64
	lastEntropy [10]byte
)

// crockford is the ULID alphabet (no I, L, O, U).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// encodeBase32 encodes 16 bytes (128 bits) as 26 base32 characters, matching
// the canonical ULID layout (2 bits of leading zero padding).
func encodeBase32(b [16]byte) string {
	var out [26]byte
	// Process the 128-bit value 5 bits at a time from the most significant end.
	// bitAt extracts bit i (0 = MSB).
	bit := func(i int) uint64 {
		return uint64(b[i/8]>>(7-uint(i%8))) & 1
	}
	for c := 0; c < 26; c++ {
		var v uint64
		for k := 0; k < 5; k++ {
			idx := c*5 + k - 2 // shift by 2: 130 slots for 128 bits, top 2 are zero
			v <<= 1
			if idx >= 0 {
				v |= bit(idx)
			}
		}
		out[c] = crockford[v]
	}
	return string(out[:])
}
