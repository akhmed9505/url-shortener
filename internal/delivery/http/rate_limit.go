package http

import (
	"container/list"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	maxTrackedIPs = 10_000
	ipIdleTTL     = 10 * time.Minute
)

type rateBucket uint8

const (
	creationBucket rateBucket = iota
	analyticsBucket
)

type ipLimits struct {
	ip        netip.Addr
	creation  *rate.Limiter
	analytics *rate.Limiter
	lastSeen  time.Time
}

type rateLimits struct {
	mu       sync.Mutex
	entries  map[netip.Addr]*list.Element
	activity list.List
	now      func() time.Time
}

func newRateLimits(now func() time.Time) *rateLimits {
	return &rateLimits{entries: make(map[netip.Addr]*list.Element), now: now}
}

func (l *rateLimits) middleware(bucket rateBucket) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if retryAfter := l.allow(remoteIP(r.RemoteAddr), bucket); retryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeError(w, r, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded", "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allow returns zero on admission or a Retry-After value in whole seconds.
func (l *rateLimits) allow(ip netip.Addr, bucket rateBucket) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// Reclaim idle entries on limited requests. Activity order avoids a full map scan.
	for oldest := l.activity.Front(); oldest != nil; oldest = l.activity.Front() {
		entry := oldest.Value.(*ipLimits)
		if now.Sub(entry.lastSeen) < ipIdleTTL {
			break
		}
		delete(l.entries, entry.ip)
		l.activity.Remove(oldest)
	}
	element := l.entries[ip]
	if element == nil {
		if len(l.entries) >= maxTrackedIPs {
			oldest := l.activity.Front().Value.(*ipLimits)
			return max(1, int(math.Ceil(oldest.lastSeen.Add(ipIdleTTL).Sub(now).Seconds())))
		}
		entry := &ipLimits{
			ip: ip, creation: rate.NewLimiter(rate.Every(6*time.Second), 5),
			analytics: rate.NewLimiter(rate.Every(time.Second), 10), lastSeen: now,
		}
		element = l.activity.PushBack(entry)
		l.entries[ip] = element
	}
	entry := element.Value.(*ipLimits)
	entry.lastSeen = now
	l.activity.MoveToBack(element)
	limiter := entry.creation
	if bucket == analyticsBucket {
		limiter = entry.analytics
	}
	if limiter.AllowN(now, 1) {
		return 0
	}
	// Inspect available tokens without reserving future tokens for rejected requests.
	delay := time.Duration((1 - limiter.TokensAt(now)) / float64(limiter.Limit()) * float64(time.Second))
	return max(1, int(math.Ceil(delay.Seconds())))
}

func remoteIP(remoteAddr string) netip.Addr {
	if addr, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return addr.Addr().Unmap().WithZone("")
	}
	addr, _ := netip.ParseAddr(remoteAddr)
	// Malformed peer addresses share one bucket instead of bypassing limits.
	return addr.Unmap().WithZone("")
}
