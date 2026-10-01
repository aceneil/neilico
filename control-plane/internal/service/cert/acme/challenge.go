package acme

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ChallengeSolver provisions and removes proof material for one ACME challenge.
type ChallengeSolver interface {
	Solve(ctx context.Context, domain, token, keyAuthorization string) error
	Cleanup(ctx context.Context, domain, token string)
}

// ChallengeStore keeps HTTP-01 key authorizations in memory until they expire.
type ChallengeStore struct {
	mu      sync.RWMutex
	entries map[string]challengeEntry
	ttl     time.Duration
}

type challengeEntry struct {
	keyAuthorization string
	expiresAt        time.Time
}

func NewChallengeStore(ttl time.Duration) *ChallengeStore {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &ChallengeStore{entries: make(map[string]challengeEntry), ttl: ttl}
}

func (s *ChallengeStore) Solve(_ context.Context, _, token, keyAuthorization string) error {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(keyAuthorization) == "" {
		return ErrInvalidChallenge
	}
	s.mu.Lock()
	s.entries[token] = challengeEntry{
		keyAuthorization: strings.TrimSpace(keyAuthorization),
		expiresAt:        time.Now().Add(s.ttl),
	}
	s.mu.Unlock()
	return nil
}

func (s *ChallengeStore) Cleanup(context.Context, string, string) {
	// HTTP-01 responses are intentionally retained until expiry so a delayed
	// validator retry can still complete after the order routine returns.
}

func (s *ChallengeStore) Get(token string) (string, bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	if !ok {
		return "", false
	}
	if !now.Before(entry.expiresAt) {
		delete(s.entries, token)
		return "", false
	}
	return entry.keyAuthorization, true
}

func (s *ChallengeStore) Delete(token string) {
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
}

func (s *ChallengeStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for token, entry := range s.entries {
		if !now.Before(entry.expiresAt) {
			delete(s.entries, token)
		}
	}
	return len(s.entries)
}

func (s *ChallengeStore) RunCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Len()
		}
	}
}

// Handler serves only RFC 8555 HTTP-01 resources.
func (s *ChallengeStore) Handler() http.Handler {
	const prefix = "/.well-known/acme-challenge/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, prefix)
		if token == "" || strings.Contains(token, "/") {
			http.NotFound(w, r)
			return
		}
		keyAuthorization, ok := s.Get(token)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(keyAuthorization))
		}
	})
}

const challengePathPrefix = "/.well-known/acme-challenge/"

// IsChallengePath reports whether a proxy request must be handled by the
// HTTP-01 challenge handler before any reverse-proxy route.
func IsChallengePath(path string) bool {
	return strings.HasPrefix(path, challengePathPrefix)
}
