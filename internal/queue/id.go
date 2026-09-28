package queue

import (
	"crypto/rand"
	"math/big"
	"strings"
	"sync"
	"time"
)

// crockford is the Crockford base32 alphabet used by ULIDs (no I, L, O, U to
// avoid transcription mistakes).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ulidGen produces sortable job ids. It is embedded in Queue so tests can
// inject a fake clock and get deterministic, still-sortable ids.
type ulidGen struct {
	mu     sync.Mutex
	now    func() time.Time
	last   [10]byte // last random component, bumped when the same ms repeats
	lastMS int64
	seeded bool
}

func newULIDGen(now func() time.Time) *ulidGen {
	return &ulidGen{now: now}
}

// New returns a 26-character ULID string: 48 bits of millisecond timestamp
// followed by 80 bits of randomness, Crockford base32 encoded. Within the
// same millisecond the random component is incremented so ids stay sortable
// and never collide inside one process.
func (g *ulidGen) New() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	ms := g.now().UTC().UnixMilli()
	if ms == g.lastMS && g.seeded {
		incBytes(&g.last)
	} else {
		_, _ = rand.Read(g.last[:])
		g.lastMS = ms
		g.seeded = true
	}

	var ts [6]byte
	ts[0] = byte(ms >> 40)
	ts[1] = byte(ms >> 32)
	ts[2] = byte(ms >> 24)
	ts[3] = byte(ms >> 16)
	ts[4] = byte(ms >> 8)
	ts[5] = byte(ms)

	var buf [16]byte
	copy(buf[:6], ts[:])
	copy(buf[6:], g.last[:])

	return encodeCrockford(buf)
}

// incBytes increments a big-endian byte array by 1, used to keep ids
// monotonic within the same millisecond.
func incBytes(b *[10]byte) {
	for i := len(b) - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return
		}
	}
}

// encodeCrockford base32-encodes 16 bytes (128 bits) into the fixed
// 26-character ULID text form (130 bits, top 2 bits of the first character
// always 0), most-significant digit first.
func encodeCrockford(b [16]byte) string {
	const chars = 26
	n := new(big.Int).SetBytes(b[:])
	digits := make([]byte, chars)
	base := big.NewInt(32)
	mod := new(big.Int)
	for i := chars - 1; i >= 0; i-- {
		n.DivMod(n, base, mod)
		digits[i] = crockford[mod.Int64()]
	}
	var sb strings.Builder
	sb.Grow(chars)
	sb.Write(digits)
	return sb.String()
}
