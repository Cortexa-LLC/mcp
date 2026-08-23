package hub

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func authedRequest(token string) *http.Request {
	r := httptest.NewRequest("GET", "/v1/graphs", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestTokenVerifier(t *testing.T) {
	v := NewTokenVerifier("s3cret")

	id, err := v.Verify(authedRequest("s3cret"))
	if err != nil {
		t.Fatalf("correct token rejected: %v", err)
	}
	if id.Subject != "token" || id.Method != "token" {
		t.Errorf("identity = %+v, want subject/method \"token\"", id)
	}

	for name, tok := range map[string]string{"wrong": "nope", "missing": ""} {
		if _, err := v.Verify(authedRequest(tok)); err == nil {
			t.Errorf("%s token accepted, want error", name)
		}
	}
}

// The empty token must never become an accept-everything verifier: an empty
// bearer header would otherwise "match" it.
func TestTokenVerifierEmptyTokenRejectsAll(t *testing.T) {
	if _, err := NewTokenVerifier("").Verify(authedRequest("")); err == nil {
		t.Fatal("empty-token verifier accepted an empty bearer, want error")
	}
}

// stubVerifier returns a fixed identity or error, for testing wrappers.
type stubVerifier struct {
	id  *Identity
	err error
}

func (s *stubVerifier) Name() string                              { return "stub" }
func (s *stubVerifier) Verify(r *http.Request) (*Identity, error) { return s.id, s.err }

func TestRequireSubjects(t *testing.T) {
	alice := &Identity{Subject: "alice", Method: "stub"}
	carol := &Identity{Subject: "guid-123", Email: "carol@example.com", Method: "stub"}
	bob := &Identity{Subject: "bob", Method: "stub"}

	v := RequireSubjects(&stubVerifier{id: alice}, []string{"alice", "carol@example.com"})
	if _, err := v.Verify(authedRequest("x")); err != nil {
		t.Errorf("allowed subject rejected: %v", err)
	}

	v = RequireSubjects(&stubVerifier{id: carol}, []string{"alice", "carol@example.com"})
	if _, err := v.Verify(authedRequest("x")); err != nil {
		t.Errorf("allowed email rejected: %v", err)
	}

	v = RequireSubjects(&stubVerifier{id: bob}, []string{"alice", "carol@example.com"})
	if _, err := v.Verify(authedRequest("x")); !errors.Is(err, ErrForbidden) {
		t.Errorf("disallowed subject: err = %v, want ErrForbidden", err)
	}

	// An authentication failure in the inner verifier must stay a 401-class
	// error, not become forbidden.
	innerErr := errors.New("bad signature")
	v = RequireSubjects(&stubVerifier{err: innerErr}, []string{"alice"})
	if _, err := v.Verify(authedRequest("x")); !errors.Is(err, innerErr) || errors.Is(err, ErrForbidden) {
		t.Errorf("inner auth failure: err = %v, want inner error without ErrForbidden", err)
	}
}
