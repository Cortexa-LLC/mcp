package hub

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// GitHubVerifier authenticates bearer tokens against GitHub's REST API.
//
// GitHub user sign-in is OAuth 2.0 without OIDC — no ID token, no discovery
// document, no JWKS — so unlike OIDCVerifier nothing can be validated
// locally: identity comes from GET /user with the caller's (opaque) token,
// and the access check from the caller's org membership (GitHub's OIDC
// tokens exist only for Actions workload identity and name a workflow, not a
// person). The login becomes the identity subject.
//
// Verification results are cached briefly (keyed by token hash) so a burst
// of searches costs one GitHub round-trip, not one per request.
type GitHubVerifier struct {
	apiBase string
	org     string
	teams   []string // non-empty = caller must be an active member of at least one
	client  *http.Client

	ttl         time.Duration // positive-result cache lifetime
	negativeTTL time.Duration // failed-verification cache lifetime

	mu    sync.Mutex
	cache map[[32]byte]githubCacheEntry
}

type githubCacheEntry struct {
	id    *Identity
	err   error
	until time.Time
}

// githubCacheCap bounds the cache. Reached only under a flood of distinct
// tokens (i.e. garbage), in which case dropping the cache is the cheap
// correct response — every entry in it is either expired or junk-adjacent.
const githubCacheCap = 4096

// NewGitHubVerifier builds a verifier requiring active membership of org,
// and of at least one of teams (slugs) when teams is non-empty. apiBase ""
// means the public API, https://api.github.com; set it for GitHub Enterprise
// (or a test server).
func NewGitHubVerifier(apiBase, org string, teams []string) (*GitHubVerifier, error) {
	if org == "" {
		return nil, errors.New("github: organization is required")
	}
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	if _, err := url.Parse(apiBase); err != nil {
		return nil, fmt.Errorf("github: invalid API base URL %q: %w", apiBase, err)
	}
	return &GitHubVerifier{
		apiBase:     apiBase,
		org:         org,
		teams:       teams,
		client:      &http.Client{Timeout: 10 * time.Second},
		ttl:         5 * time.Minute,
		negativeTTL: 30 * time.Second,
		cache:       make(map[[32]byte]githubCacheEntry),
	}, nil
}

func (v *GitHubVerifier) Name() string { return "github" }

func (v *GitHubVerifier) Verify(r *http.Request) (*Identity, error) {
	raw := bearerToken(r)
	if raw == "" {
		return nil, errors.New("missing bearer token")
	}
	// Tokens are cached and compared by hash only — a credential should not
	// sit in memory as a map key.
	key := sha256.Sum256([]byte(raw))

	v.mu.Lock()
	if e, ok := v.cache[key]; ok && time.Now().Before(e.until) {
		v.mu.Unlock()
		return e.id, e.err
	}
	v.mu.Unlock()

	id, err := v.verifyUncached(raw)

	v.mu.Lock()
	if len(v.cache) >= githubCacheCap {
		v.cache = make(map[[32]byte]githubCacheEntry)
	}
	ttl := v.ttl
	if err != nil {
		ttl = v.negativeTTL
	}
	v.cache[key] = githubCacheEntry{id: id, err: err, until: time.Now().Add(ttl)}
	v.mu.Unlock()
	return id, err
}

func (v *GitHubVerifier) verifyUncached(token string) (*Identity, error) {
	var user struct {
		Login string `json:"login"`
		Email string `json:"email"`
	}
	status, err := v.get(token, "/user", &user)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusUnauthorized:
		return nil, errors.New("github rejected the token")
	case status != http.StatusOK:
		return nil, fmt.Errorf("github /user returned %d", status)
	case user.Login == "":
		return nil, errors.New("github /user returned no login")
	}

	var membership struct {
		State string `json:"state"`
	}
	status, err = v.get(token, "/user/memberships/orgs/"+url.PathEscape(v.org), &membership)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusNotFound:
		return nil, fmt.Errorf("github:%s is not a member of the %s organization: %w", user.Login, v.org, ErrForbidden)
	case status == http.StatusForbidden:
		// The usual cause is a token without read:org — the caller can fix
		// that themselves, so say so.
		return nil, fmt.Errorf("github:%s: cannot read %s org membership (does the token have the read:org scope?): %w", user.Login, v.org, ErrForbidden)
	case status != http.StatusOK:
		return nil, fmt.Errorf("github org membership check returned %d", status)
	case membership.State != "active":
		return nil, fmt.Errorf("github:%s's membership of %s is %q, not active: %w", user.Login, v.org, membership.State, ErrForbidden)
	}

	groups := []string{v.org}
	if len(v.teams) > 0 {
		matched := ""
		for _, team := range v.teams {
			var tm struct {
				State string `json:"state"`
			}
			path := "/orgs/" + url.PathEscape(v.org) + "/teams/" + url.PathEscape(team) +
				"/memberships/" + url.PathEscape(user.Login)
			status, err := v.get(token, path, &tm)
			if err != nil {
				return nil, err
			}
			if status == http.StatusOK && tm.State == "active" {
				matched = team
				break
			}
		}
		if matched == "" {
			return nil, fmt.Errorf("github:%s is not a member of any allowed %s team: %w", user.Login, v.org, ErrForbidden)
		}
		groups = append(groups, v.org+"/"+matched)
	}

	return &Identity{
		Subject: user.Login,
		Email:   user.Email,
		Groups:  groups,
		Method:  "github",
	}, nil
}

// get performs an authenticated GitHub API GET and decodes a 200 body into
// out. Non-200 statuses are returned for the caller to interpret; only
// transport failures are errors.
func (v *GitHubVerifier) get(token, path string, out any) (int, error) {
	req, err := http.NewRequest("GET", v.apiBase+path, nil)
	if err != nil {
		return 0, fmt.Errorf("github request %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := v.client.Do(req)
	if err != nil {
		// The wrapped error can embed the request URL but never the token —
		// and this message is client-visible, so keep it that way.
		return 0, fmt.Errorf("github api unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return 0, fmt.Errorf("github %s: decode response: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}
