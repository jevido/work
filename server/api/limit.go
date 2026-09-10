package api

import (
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// What one caller may do. A desktop replica syncing hard does a handful of
// requests a second; a viewer polling does one every few seconds. Ten a second
// is far above both and far below anything that costs the database.
const (
	perSecond = 10
	burst     = 40

	// idleFor is how long a caller has to be quiet before its bucket is
	// forgotten. Longer than any client's polling interval, so an active caller
	// is never re-created at full burst.
	idleFor = 10 * time.Minute
	// sweepEvery is how often idle buckets are cleared out. The sweep is the
	// only thing that bounds this map, and the map is the only thing here that
	// grows with the number of callers seen.
	sweepEvery = time.Minute
)

// limiter rate-limits per caller.
//
// It is in memory and therefore per instance, which is worth being clear about:
// running two of these behind a load balancer gives each caller two budgets. It
// is a guard against a loop in a client, not a security control, and a shared
// budget would mean a round trip to Redis on every request to defend against
// something no attacker has to go through.
type limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
	now       func() time.Time // swapped in tests
}

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newLimiter() *limiter {
	return &limiter{buckets: make(map[string]*bucket), now: time.Now}
}

// allow charges one request to a caller. When it refuses, it also says how long
// to wait, in the seconds a Retry-After header wants.
func (l *limiter) allow(who string) (retryAfter string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	found, exists := l.buckets[who]
	if !exists {
		found = &bucket{limiter: rate.NewLimiter(perSecond, burst)}
		l.buckets[who] = found
	}
	found.lastSeen = now

	if found.limiter.AllowN(now, 1) {
		return "", true
	}
	// Round up, because a Retry-After of zero invites an immediate retry that
	// is refused again.
	wait := found.limiter.ReserveN(now, 1)
	seconds := int(wait.DelayFrom(now)/time.Second) + 1
	wait.CancelAt(now)
	return strconv.Itoa(seconds), false
}

// sweep forgets callers that have gone quiet. It runs under the caller's lock
// and at most once a minute, so it costs nothing per request: a server with a
// hundred thousand buckets walks them once, not on every call.
func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < sweepEvery {
		return
	}
	l.lastSweep = now
	for who, found := range l.buckets {
		if now.Sub(found.lastSeen) > idleFor {
			delete(l.buckets, who)
		}
	}
}
