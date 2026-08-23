package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitHub is an in-process GitHub REST API: a token table drives /user,
// and per-login org/team membership drives the membership endpoints.
type fakeGitHub struct {
	srv       *httptest.Server
	userCalls atomic.Int64

	tokens map[string]fakeGitHubUser // token -> user
}

type fakeGitHubUser struct {
	login    string
	email    string
	orgState string   // membership state in the verifier's org; "" = 404
	orgErr   int      // non-zero: membership endpoint returns this status
	teams    []string // team slugs with active membership
}

func newFakeGitHub(t *testing.T, org string, tokens map[string]fakeGitHubUser) *fakeGitHub {
	t.Helper()
	gh := &fakeGitHub{tokens: tokens}

	auth := func(r *http.Request) (fakeGitHubUser, bool) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			return fakeGitHubUser{}, false
		}
		u, ok := gh.tokens[tok]
		return u, ok
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		gh.userCalls.Add(1)
		u, ok := auth(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"login": u.login, "email": u.email})
	})
	mux.HandleFunc("GET /user/memberships/orgs/"+org, func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if u.orgErr != 0 {
			w.WriteHeader(u.orgErr)
			return
		}
		if u.orgState == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"state": u.orgState})
	})
	mux.HandleFunc("GET /orgs/"+org+"/teams/{team}/memberships/{login}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.PathValue("login") != u.login {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		for _, team := range u.teams {
			if team == r.PathValue("team") {
				json.NewEncoder(w).Encode(map[string]string{"state": "active"})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	gh.srv = httptest.NewServer(mux)
	t.Cleanup(gh.srv.Close)
	return gh
}

func (gh *fakeGitHub) verifier(t *testing.T, org string, teams []string) *GitHubVerifier {
	t.Helper()
	v, err := NewGitHubVerifier(gh.srv.URL, org, teams)
	if err != nil {
		t.Fatalf("NewGitHubVerifier: %v", err)
	}
	return v
}

func TestGitHubReadAuth(t *testing.T) {
	gh := newFakeGitHub(t, "cortexa", map[string]fakeGitHubUser{
		"tok-alice":   {login: "alice", email: "alice@example.com", orgState: "active"},
		"tok-pending": {login: "pat", orgState: "pending"},
		"tok-outside": {login: "outsider"},
		"tok-noscope": {login: "sam", orgErr: http.StatusForbidden},
	})
	srv := NewServerWithAuth(t.TempDir(), gh.verifier(t, "cortexa", nil), nil, "dev")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := ts.URL + "/v1/graphs"

	cases := map[string]struct {
		token string
		want  int
	}{
		"org member":         {"tok-alice", http.StatusOK},
		"missing":            {"", http.StatusUnauthorized},
		"unknown token":      {"tok-bogus", http.StatusUnauthorized},
		"pending membership": {"tok-pending", http.StatusForbidden},
		"not in org":         {"tok-outside", http.StatusForbidden},
		"missing read:org":   {"tok-noscope", http.StatusForbidden},
	}
	for name, tc := range cases {
		if got := getStatus(t, url, tc.token); got != tc.want {
			t.Errorf("%s: status = %d, want %d", name, got, tc.want)
		}
	}
}

func TestGitHubTeamRequirement(t *testing.T) {
	gh := newFakeGitHub(t, "cortexa", map[string]fakeGitHubUser{
		"tok-alice": {login: "alice", orgState: "active", teams: []string{"platform"}},
		"tok-bob":   {login: "bob", orgState: "active", teams: []string{"design"}},
	})
	v := gh.verifier(t, "cortexa", []string{"platform", "kg"})

	id, err := v.Verify(authedRequest("tok-alice"))
	if err != nil {
		t.Fatalf("team member rejected: %v", err)
	}
	if len(id.Groups) != 2 || id.Groups[0] != "cortexa" || id.Groups[1] != "cortexa/platform" {
		t.Errorf("groups = %v, want [cortexa cortexa/platform]", id.Groups)
	}

	if _, err := v.Verify(authedRequest("tok-bob")); err == nil || !strings.Contains(err.Error(), "team") {
		t.Errorf("org member outside allowed teams: err = %v, want team-membership error", err)
	}
	if got := statusFor(t, v, "tok-bob"); got != http.StatusForbidden {
		t.Errorf("outside allowed teams: status = %d, want 403", got)
	}
}

// statusFor runs a verifier inside a real handler stack and reports the
// status an authenticated read would get.
func statusFor(t *testing.T, v Verifier, token string) int {
	t.Helper()
	srv := NewServerWithAuth(t.TempDir(), v, nil, "dev")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	return getStatus(t, ts.URL+"/v1/graphs", token)
}

func TestGitHubIdentity(t *testing.T) {
	gh := newFakeGitHub(t, "cortexa", map[string]fakeGitHubUser{
		"tok-alice": {login: "alice", email: "alice@example.com", orgState: "active"},
	})
	id, err := gh.verifier(t, "cortexa", nil).Verify(authedRequest("tok-alice"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Subject != "alice" || id.Email != "alice@example.com" || id.Method != "github" {
		t.Errorf("identity = %+v, want subject alice / email alice@example.com / method github", id)
	}
	if len(id.Groups) != 1 || id.Groups[0] != "cortexa" {
		t.Errorf("groups = %v, want [cortexa]", id.Groups)
	}
}

func TestGitHubVerificationCache(t *testing.T) {
	gh := newFakeGitHub(t, "cortexa", map[string]fakeGitHubUser{
		"tok-alice": {login: "alice", orgState: "active"},
	})
	v := gh.verifier(t, "cortexa", nil)

	for range 3 {
		if _, err := v.Verify(authedRequest("tok-alice")); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
	if calls := gh.userCalls.Load(); calls != 1 {
		t.Errorf("3 verifications hit /user %d times, want 1 (cached)", calls)
	}

	// Failures are cached too (briefly), so a misbehaving client cannot make
	// the hub hammer GitHub.
	for range 3 {
		if _, err := v.Verify(authedRequest("tok-bogus")); err == nil {
			t.Fatal("bogus token accepted")
		}
	}
	if calls := gh.userCalls.Load(); calls != 2 {
		t.Errorf("bogus-token verifications hit /user %d times total, want 2", calls)
	}

	// An expired entry re-verifies rather than serving stale state.
	expiring := gh.verifier(t, "cortexa", nil)
	expiring.ttl = 0
	base := gh.userCalls.Load()
	for range 2 {
		if _, err := expiring.Verify(authedRequest("tok-alice")); err != nil {
			t.Fatalf("Verify with zero ttl: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if calls := gh.userCalls.Load() - base; calls != 2 {
		t.Errorf("zero-ttl verifications hit /user %d times, want 2 (no stale serving)", calls)
	}
}

func TestNewGitHubVerifierConfigErrors(t *testing.T) {
	if _, err := NewGitHubVerifier("", "", nil); err == nil {
		t.Error("empty org accepted, want error")
	}
}
