package events

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// crockford is the Crockford base32 alphabet used by ULID-style ids:
// case-insensitive, excludes I, L, O, U to avoid transcription mistakes.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var idGen struct {
	mu      sync.Mutex
	lastMS  int64
	lastRnd [10]byte
	init    bool
}

// newID returns a 26-character, lexicographically sortable identifier in
// the ULID layout: a 48-bit millisecond Unix timestamp followed by 80 bits
// of randomness, Crockford base32 encoded. IDs generated within the same
// millisecond increment the random part (monotonic) so a tight loop of
// newID() calls still sorts in call order, matching the "IDs are ULIDs
// (sortable)" rule in ARCHITECTURE.md §4.
//
// This is a small self-contained encoder rather than a dependency: no
// ULID/UUID module was present in the local module cache, and adding one
// would touch the shared go.mod while sibling tickets are also mid-flight
// on it.
func newID() string {
	idGen.mu.Lock()
	defer idGen.mu.Unlock()

	ms := time.Now().UnixMilli()
	var rnd [10]byte
	if idGen.init && ms == idGen.lastMS {
		rnd = idGen.lastRnd
		incRandom(&rnd)
	} else {
		if _, err := rand.Read(rnd[:]); err != nil {
			// crypto/rand.Read does not fail on any platform this project
			// targets; fall back to a timestamp-derived value instead of
			// panicking so id generation never becomes a hard failure.
			for i := range rnd {
				rnd[i] = byte(ms >> (uint(i%8) * 8))
			}
		}
	}
	idGen.lastMS = ms
	idGen.lastRnd = rnd
	idGen.init = true

	var buf [16]byte
	buf[0] = byte(ms >> 40)
	buf[1] = byte(ms >> 32)
	buf[2] = byte(ms >> 24)
	buf[3] = byte(ms >> 16)
	buf[4] = byte(ms >> 8)
	buf[5] = byte(ms)
	copy(buf[6:], rnd[:])
	return encode128(buf)
}

func incRandom(b *[10]byte) {
	for i := len(b) - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return
		}
	}
}

// encode128 renders a 128-bit big-endian value as 26 Crockford base32
// characters (130 bits of capacity; the top 2 bits of the first character
// are always 0), matching the standard ULID text encoding.
func encode128(buf [16]byte) string {
	hi := binary.BigEndian.Uint64(buf[:8])
	lo := binary.BigEndian.Uint64(buf[8:])

	out := make([]byte, 26)
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&0x1f]
		lo = (lo >> 5) | ((hi & 0x1f) << 59)
		hi >>= 5
	}
	return string(out)
}
