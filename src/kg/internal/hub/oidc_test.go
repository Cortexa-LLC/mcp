package hub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIDP is an in-process OIDC issuer: it serves a discovery document and a
// JWKS for a generated RSA key, and mints RS256 tokens signed with that key.
type fakeIDP struct {
	t   *testing.T
	key *rsa.PrivateKey
	srv *httptest.Server
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &fakeIDP{t: t, key: key}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":   idp.srv.URL,
			"jwks_uri": idp.srv.URL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &idp.key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig",
		}}})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

// token mints a signed JWT with sane defaults, overridden by override.
// Passing a nil value in override deletes the claim.
func (idp *fakeIDP) token(override map[string]any) string {
	return signToken(idp.t, idp.key, idp.srv.URL, override)
}

func signToken(t *testing.T, key *rsa.PrivateKey, issuer string, override map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"iss": issuer,
		"sub": "alice",
		"aud": "kg-hub",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
	for k, v := range override {
		if v == nil {
			delete(claims, k)
			continue
		}
		claims[k] = v
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test"))
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return raw
}

func (idp *fakeIDP) verifier(t *testing.T) *OIDCVerifier {
	t.Helper()
	v, err := NewOIDCVerifier(context.Background(), idp.srv.URL, "kg-hub")
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}
	return v
}

func getStatus(t *testing.T, url, token string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestOIDCReadAuth(t *testing.T) {
	idp := newFakeIDP(t)
	srv := NewServerWithAuth(t.TempDir(), idp.verifier(t), nil, "dev")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := ts.URL + "/v1/graphs"

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	cases := map[string]struct {
		token string
		want  int
	}{
		"valid":          {idp.token(nil), http.StatusOK},
		"missing":        {"", http.StatusUnauthorized},
		"garbage":        {"not-a-jwt", http.StatusUnauthorized},
		"expired":        {idp.token(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}), http.StatusUnauthorized},
		"wrong audience": {idp.token(map[string]any{"aud": "other-app"}), http.StatusUnauthorized},
		"wrong issuer":   {signToken(t, idp.key, "https://evil.example", nil), http.StatusUnauthorized},
		"unknown key":    {signToken(t, otherKey, idp.srv.URL, nil), http.StatusUnauthorized},
	}
	for name, tc := range cases {
		if got := getStatus(t, url, tc.token); got != tc.want {
			t.Errorf("%s token: status = %d, want %d", name, got, tc.want)
		}
	}
}

func TestOIDCVerifyIdentityClaims(t *testing.T) {
	idp := newFakeIDP(t)
	v := idp.verifier(t)

	tok := idp.token(map[string]any{
		"sub":    "guid-42",
		"email":  "alice@example.com",
		"groups": []string{"eng", "platform"},
	})
	id, err := v.Verify(authedRequest(tok))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Subject != "guid-42" || id.Email != "alice@example.com" || id.Method != "oidc" {
		t.Errorf("identity = %+v, want sub guid-42 / email alice@example.com / method oidc", id)
	}
	if len(id.Groups) != 2 || id.Groups[0] != "eng" || id.Groups[1] != "platform" {
		t.Errorf("groups = %v, want [eng platform]", id.Groups)
	}

	// Tokens without the optional claims still authenticate.
	id, err = v.Verify(authedRequest(idp.token(nil)))
	if err != nil {
		t.Fatalf("Verify without optional claims: %v", err)
	}
	if id.Subject != "alice" || id.Email != "" || len(id.Groups) != 0 {
		t.Errorf("identity = %+v, want bare subject alice", id)
	}
}

// Seeding through an allowlist: a disallowed subject gets 403; an allowed one
// passes auth and reaches the handler (which then 400s on the missing seed
// headers — proof the request got past the verifier).
func TestOIDCSeedSubjectAllowlist(t *testing.T) {
	idp := newFakeIDP(t)
	seedAuth := RequireSubjects(idp.verifier(t), []string{"alice", "carol@example.com"})
	srv := NewServerWithAuth(t.TempDir(), nil, seedAuth, "dev")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	put := func(token string) (int, string) {
		req, _ := http.NewRequest("PUT", ts.URL+"/v1/graphs/demo", strings.NewReader(""))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT: %v", err)
		}
		defer resp.Body.Close()
		var body struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body.Error
	}

	if got, msg := put(idp.token(map[string]any{"sub": "bob"})); got != http.StatusForbidden {
		t.Errorf("disallowed subject: status = %d (%s), want 403", got, msg)
	}
	if got, msg := put(idp.token(nil)); got != http.StatusBadRequest || !strings.Contains(msg, "X-KG-Commit") {
		t.Errorf("allowed subject: status = %d (%s), want 400 for missing X-KG-Commit", got, msg)
	}
	if got, msg := put(idp.token(map[string]any{"sub": "guid-9", "email": "carol@example.com"})); got != http.StatusBadRequest {
		t.Errorf("allowed email: status = %d (%s), want 400 for missing X-KG-Commit", got, msg)
	}
}

func TestNewOIDCVerifierConfigErrors(t *testing.T) {
	ctx := context.Background()
	idp := newFakeIDP(t)
	if _, err := NewOIDCVerifier(ctx, "", "aud"); err == nil {
		t.Error("empty issuer accepted, want error")
	}
	if _, err := NewOIDCVerifier(ctx, idp.srv.URL, ""); err == nil {
		t.Error("empty audience accepted, want error")
	}
	if _, err := NewOIDCVerifier(ctx, "http://127.0.0.1:1/nowhere", "aud"); err == nil {
		t.Error("unreachable issuer accepted, want error at construction")
	}
}
