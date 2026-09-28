package revenue

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// crockford is the Crockford base32 alphabet used by ULID-style ids:
// case-insensitive, excludes I, L, O, U to avoid transcription mistakes.
// Matches ARCHITECTURE.md §4 ("IDs are ULIDs (sortable)").
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var idGen struct {
	mu      sync.Mutex
	lastMS  int64
	lastRnd [10]byte
	init    bool
}

// newID returns a 26-character, lexicographically sortable ULID-style id.
// Each package in this repo keeps its own small copy of this encoder
// (internal/events, internal/queue) rather than sharing one across a
// shared go.mod while sibling tickets are mid-flight on it — see
// internal/events/id.go's doc comment for the same reasoning.
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
