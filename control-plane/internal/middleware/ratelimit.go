package middleware

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"neilico/control-plane/internal/auth"
)

type rateBucket struct {
	tokens  float64
	updated time.Time
}

type Limiter struct {
	mu      sync.Mutex
	rps     float64
	burst   float64
	buckets map[string]rateBucket
	now     func() time.Time
}

func NewLimiter(rps float64, burst int) *Limiter {
	return &Limiter{
		rps:     rps,
		burst:   float64(burst),
		buckets: make(map[string]rateBucket),
		now:     time.Now,
	}
}

func (l *Limiter) Allow(key string) (time.Duration, bool) {
	if l == nil {
		return 0, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok {
		bucket = rateBucket{tokens: l.burst, updated: now}
	}
	elapsed := now.Sub(bucket.updated).Seconds()
	if elapsed > 0 {
		bucket.tokens = math.Min(l.burst, bucket.tokens+elapsed*l.rps)
	}
	bucket.updated = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		l.buckets[key] = bucket
		return 0, true
	}
	l.buckets[key] = bucket
	retry := time.Duration(math.Ceil((1 - bucket.tokens) / l.rps * float64(time.Second)))
	if retry < time.Second {
		retry = time.Second
	}
	return retry, false
}

func RateLimit(limiter *Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if limiter == nil || RateLimitExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		key := requestRateKey(r)
		retry, allowed := limiter.Allow(key)
		if allowed {
			next.ServeHTTP(w, r)
			return
		}
		seconds := int(math.Ceil(retry.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "request rate limit exceeded")
	})
}

func RateLimitExempt(path string) bool {
	return path == "/healthz" || path == "/metrics" ||
		strings.HasPrefix(path, "/.well-known/acme-challenge/")
}

func requestRateKey(r *http.Request) string {
	if principal, ok := PrincipalFromContext(r.Context()); ok {
		switch principal.AuthMethod {
		case auth.AuthMethodAPIToken:
			return "api-token:" + principal.APITokenID.String()
		case auth.AuthMethodJWT:
			return "jwt-user:" + principal.UserID.String()
		}
	}
	if token := bearerToken(r); auth.IsAPIToken(token) {
		return "api-token-credential:" + auth.HashAPIToken(token)
	}
	return "client:" + clientIP(r)
}
