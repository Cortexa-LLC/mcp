package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCVerifier validates bearer JWTs against an OIDC issuer.
//
// Construction fetches the issuer's discovery document
// (/.well-known/openid-configuration) and takes jwks_uri from it; Verify
// checks the JWT signature against the published keys along with iss, aud and
// exp. `sub` becomes the identity. Entra ID, Okta, Keycloak, Auth0 and Google
// are all reachable this way — changing provider is configuration, not code.
type OIDCVerifier struct {
	issuer   string
	verifier *oidc.IDTokenVerifier
}

// NewOIDCVerifier builds a verifier for tokens issued by issuer to audience.
// It fetches the discovery document immediately, so a hub misconfigured with
// an unreachable issuer fails at startup rather than on the first request.
// The audience is required: skipping the aud check would accept any token the
// issuer ever minted for any application.
func NewOIDCVerifier(ctx context.Context, issuer, audience string) (*OIDCVerifier, error) {
	if issuer == "" {
		return nil, errors.New("oidc: issuer URL is required")
	}
	if audience == "" {
		return nil, errors.New("oidc: audience is required")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", issuer, err)
	}
	return &OIDCVerifier{
		issuer:   issuer,
		verifier: provider.Verifier(&oidc.Config{ClientID: audience}),
	}, nil
}

func (v *OIDCVerifier) Name() string { return "oidc" }

// Issuer returns the issuer URL this verifier trusts, for status output.
func (v *OIDCVerifier) Issuer() string { return v.issuer }

func (v *OIDCVerifier) Verify(r *http.Request) (*Identity, error) {
	raw := bearerToken(r)
	if raw == "" {
		return nil, errors.New("missing bearer token")
	}
	tok, err := v.verifier.Verify(r.Context(), raw)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	// email and groups are optional enrichment — absent claims leave the
	// zero values, which every consumer treats as "unknown".
	var claims struct {
		Email  string   `json:"email"`
		Groups []string `json:"groups"`
	}
	_ = tok.Claims(&claims)
	return &Identity{
		Subject: tok.Subject,
		Email:   claims.Email,
		Groups:  claims.Groups,
		Method:  "oidc",
	}, nil
}
