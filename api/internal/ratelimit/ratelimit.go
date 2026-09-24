// Package ratelimit is a dependency-free, in-memory, per-client-IP token-bucket
// rate limiter for the handful of UNAUTHENTICATED endpoints that mint or check a
// credential -- login and the field-app login exchange. Those paths already carry
// a per-account lockout (auth/login.go), which stops guessing at ONE account but
// not a spray spread thin across many accounts from a single source, nor a flood
// aimed at locking accounts out. A coarse per-IP cap is the complementary control.
//
// It is deliberately process-local and store-less: each server instance limits
// only the traffic it personally sees. For a single-instance or sticky-routed
// deployment that is the whole picture; behind a fan-out load balancer, front it
// with the balancer's own limiter too. Keying is on the transport peer address
// (RemoteAddr), never a client-settable header like X-Forwarded-For, so a caller
// can't rotate the key by spoofing a header -- the trade-off is that many clients
// behind one NAT share a bucket, which for a small internal user base is fine.
package ratelimit

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Limiter caps requests per client IP. The zero value is not usable; build one
// with New. It is safe for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	burst   float64       // maximum tokens (the allowed short burst)
	refill  float64       // tokens replenished per second
	ttl     time.Duration // idle age after which a bucket is swept
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New builds a limiter that lets a client make up to burst requests immediately
// and then perMinute requests per minute sustained, per IP. It starts a
// background sweeper that evicts idle buckets, so the map can't grow without
// bound under a spray from many distinct source addresses.
func New(perMinute, burst int) *Limiter {
	if perMinute <= 0 {
		perMinute = 1
	}
	if burst <= 0 {
		burst = 1
	}
	l := &Limiter{
		buckets: make(map[string]*bucket),
		burst:   float64(burst),
		refill:  float64(perMinute) / 60.0,
		ttl:     10 * time.Minute,
	}
	go l.sweep()
	return l
}

// allow reports whether a request from key is permitted now, spending a token
// when it is. A brand-new key starts full (minus the one it spends).
func (l *Limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}
	// Refill for the elapsed time, capped at burst.
	b.tokens += now.Sub(b.last).Seconds() * l.refill
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep periodically drops buckets untouched for longer than ttl, bounding memory.
func (l *Limiter) sweep() {
	t := time.NewTicker(l.ttl)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-l.ttl)
		l.mu.Lock()
		for k, b := range l.buckets {
			if b.last.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// Middleware is a net/http (chi-compatible) middleware that rejects a client
// over the rate with 429 + Retry-After. Use it via chi's r.With(lim.Middleware).
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r), time.Now()) {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"too many attempts; please wait and try again"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the transport peer address (host part of RemoteAddr), the un-spoofable
// key. It intentionally ignores X-Forwarded-For / X-Real-IP.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
