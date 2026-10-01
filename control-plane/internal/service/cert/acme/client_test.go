package acme

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type countingTransport struct {
	registerCalls int
	obtainCalls   int
	revokeCalls   int
	certPEM       []byte
	keyPEM        []byte
	notAfter      time.Time
	err           error
}

func (t *countingTransport) Register(context.Context, string) error {
	t.registerCalls++
	return t.err
}

func (t *countingTransport) Obtain(context.Context, string, ChallengeSolver) ([]byte, []byte, time.Time, error) {
	t.obtainCalls++
	if t.err != nil {
		return nil, nil, time.Time{}, t.err
	}
	return t.certPEM, t.keyPEM, t.notAfter, nil
}

func (t *countingTransport) Revoke(context.Context, []byte) error {
	t.revokeCalls++
	return t.err
}

func (n nopSolver) Solve(context.Context, string, string, string) error { return nil }
func (n nopSolver) Cleanup(context.Context, string, string)             {}

type nopSolver struct{}

func TestDisabledAndTOSGuardMakeNoRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "disabled", cfg: Config{Challenge: ChallengeHTTP01}, want: "acme_disabled"},
		{name: "tos", cfg: Config{Enabled: true, Challenge: ChallengeHTTP01}, want: "acme_tos_not_accepted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &countingTransport{}
			client := New(tc.cfg, transport)
			if err := client.Validate(ChallengeHTTP01); !errors.Is(err, ErrDisabled) && !errors.Is(err, ErrTOSNotAccepted) {
				t.Fatalf("Validate error = %v", err)
			}
			if got := ErrorCode(errForCode(client.Validate(ChallengeHTTP01))); got != tc.want {
				t.Fatalf("error code = %q, want %q", got, tc.want)
			}
			_ = client.Register(context.Background(), "ops@example.test")
			_, _, _, _ = client.Obtain(context.Background(), "app.example.test", nopSolver{})
			_ = client.Revoke(context.Background(), []byte("cert"))
			if transport.registerCalls != 0 || transport.obtainCalls != 0 || transport.revokeCalls != 0 {
				t.Fatalf("transport calls = %d/%d/%d, want 0/0/0", transport.registerCalls, transport.obtainCalls, transport.revokeCalls)
			}
		})
	}
}

func errForCode(err error) error { return err }

func TestDNS01LeavesInterfaceNotImplementedWithoutRequest(t *testing.T) {
	t.Parallel()
	transport := &countingTransport{}
	client := New(Config{Enabled: true, AgreeTOS: true, Challenge: ChallengeDNS01}, transport)
	_, _, _, err := client.Obtain(context.Background(), "app.example.test", nopSolver{})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Obtain error = %v, want ErrNotImplemented", err)
	}
	if ErrorCode(err) != "acme_challenge_not_implemented" || transport.obtainCalls != 0 {
		t.Fatalf("code = %q calls = %d", ErrorCode(err), transport.obtainCalls)
	}
}

func TestObtainUsesInjectedTransport(t *testing.T) {
	t.Parallel()
	wantExpiry := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	transport := &countingTransport{
		certPEM:  []byte("cert"),
		keyPEM:   []byte("key"),
		notAfter: wantExpiry,
	}
	client := New(Config{Enabled: true, AgreeTOS: true, Challenge: ChallengeHTTP01}, transport)
	certPEM, keyPEM, notAfter, err := client.Obtain(context.Background(), "App.Example.Test", nopSolver{})
	if err != nil {
		t.Fatal(err)
	}
	if string(certPEM) != "cert" || string(keyPEM) != "key" || !notAfter.Equal(wantExpiry) || transport.obtainCalls != 1 {
		t.Fatalf("unexpected obtain result: cert=%q key=%q expiry=%v calls=%d", certPEM, keyPEM, notAfter, transport.obtainCalls)
	}
}

func TestChallengeStoreAndHandler(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore(time.Hour)
	if err := store.Solve(context.Background(), "app.example.test", "known-token", "known-token.key-auth"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(store.Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/.well-known/acme-challenge/known-token")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := readAll(response)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/plain" || body != "known-token.key-auth" {
		t.Fatalf("known challenge response: status=%d type=%q body=%q", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	response, err = http.Get(server.URL + "/.well-known/acme-challenge/unknown-token")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown challenge status = %d, want 404", response.StatusCode)
	}
	_ = response.Body.Close()
}

func TestChallengeStoreExpiresEntries(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore(10 * time.Millisecond)
	if err := store.Solve(context.Background(), "app.example.test", "token", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if value, ok := store.Get("token"); !ok || value != "key-auth" {
		t.Fatalf("Get before expiry = %q, %v", value, ok)
	}
	time.Sleep(25 * time.Millisecond)
	if _, ok := store.Get("token"); ok {
		t.Fatal("expired challenge remained readable")
	}
	if store.Len() != 0 {
		t.Fatal("expired challenge remained counted")
	}
}

func TestChallengeHandlerRejectsNonGET(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore(time.Hour)
	request := httptest.NewRequest(http.MethodPost, "/.well-known/acme-challenge/token", strings.NewReader("x"))
	recorder := httptest.NewRecorder()
	store.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("response = %d Allow=%q", recorder.Code, recorder.Header().Get("Allow"))
	}
}

func readAll(response *http.Response) (string, error) {
	defer response.Body.Close()
	var builder strings.Builder
	buffer := make([]byte, 256)
	for {
		n, err := response.Body.Read(buffer)
		builder.Write(buffer[:n])
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return "", err
			}
			return builder.String(), nil
		}
	}
}
