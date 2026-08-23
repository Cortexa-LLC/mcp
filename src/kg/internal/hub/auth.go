package hub

import (
	"errors"
	"fmt"
	"net/http"
)

// Identity is an authenticated caller, as established by a Verifier.
type Identity struct {
	// Subject is the stable identifier the verifier established — an OIDC
	// `sub`, or "token" for the shared-secret verifier. It is what
	// authorization and audit lines key on.
	Subject string
	// Email, when the verifier can supply it, exists because subjects from
	// enterprise IdPs are often opaque GUIDs: it makes audit lines readable
	// and may be used interchangeably with Subject in allowlists.
	Email string
	// Groups carries group/team memberships when the verifier can supply
	// them. Empty is normal.
	Groups []string
	// Method names the verifier that produced this identity ("token",
	// "oidc", ...).
	Method string
}

// String renders the identity for audit logs. A nil identity is an
// unauthenticated caller on an open surface.
func (id *Identity) String() string {
	if id == nil {
		return "anonymous"
	}
	if id.Email != "" && id.Email != id.Subject {
		return fmt.Sprintf("%s:%s (%s)", id.Method, id.Subject, id.Email)
	}
	return fmt.Sprintf("%s:%s", id.Method, id.Subject)
}

// Verifier authenticates an HTTP request and returns who made it.
//
// A Verifier answers only "who is this?"; what they may do is the server's
// (or a wrapping Verifier's) decision. Errors are reported to the client as
// 401 unless they wrap ErrForbidden, which means "authenticated, but not
// allowed" and reports as 403.
type Verifier interface {
	// Name identifies the verifier kind for logs and status output.
	Name() string
	// Verify authenticates r. A nil error means the request is
	// authenticated as the returned identity.
	Verify(r *http.Request) (*Identity, error)
}

// ErrForbidden marks a Verify error as an authorization failure (403) rather
// than an authentication failure (401): the caller proved who they are, and
// who they are is not allowed.
var ErrForbidden = errors.New("forbidden")

// --- token verifier ---

// TokenVerifier is the shared-secret scheme as a Verifier: one bearer token,
// one degenerate identity. It keeps existing token deployments working and is
// the interface's trivial reference implementation.
type TokenVerifier struct {
	token string
}

// NewTokenVerifier returns a Verifier accepting exactly token. The empty
// token is not a valid configuration — "no token" is expressed as no
// verifier, not a verifier that matches "".
func NewTokenVerifier(token string) *TokenVerifier {
	return &TokenVerifier{token: token}
}

func (v *TokenVerifier) Name() string { return "token" }

func (v *TokenVerifier) Verify(r *http.Request) (*Identity, error) {
	if v.token == "" || !tokenMatches(bearerToken(r), v.token) {
		return nil, errors.New("missing or invalid bearer token")
	}
	return &Identity{Subject: "token", Method: "token"}, nil
}

// --- subject allowlist ---

// RequireSubjects wraps a Verifier with an allowlist: identities whose
// Subject or Email is not listed verify successfully but are rejected with
// ErrForbidden. Email is accepted because enterprise IdP subjects are often
// GUIDs nobody writes allowlists in.
func RequireSubjects(inner Verifier, subjects []string) Verifier {
	allowed := make(map[string]bool, len(subjects))
	for _, s := range subjects {
		if s != "" {
			allowed[s] = true
		}
	}
	return &subjectFilter{inner: inner, allowed: allowed}
}

type subjectFilter struct {
	inner   Verifier
	allowed map[string]bool
}

func (f *subjectFilter) Name() string { return f.inner.Name() }

func (f *subjectFilter) Verify(r *http.Request) (*Identity, error) {
	id, err := f.inner.Verify(r)
	if err != nil {
		return nil, err
	}
	if f.allowed[id.Subject] || (id.Email != "" && f.allowed[id.Email]) {
		return id, nil
	}
	return nil, fmt.Errorf("%s is not on the allowed-subjects list: %w", id, ErrForbidden)
}
