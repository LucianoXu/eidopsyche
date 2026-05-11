// internal/claudeauth/wrap.go
//
// WrapSetupToken is the "I have a sk-ant-oat01-... string, just install
// it" install path — the most operator-friendly of all the login flows.
//
// Claude Code's `claude setup-token` interactive flow exchanges a
// browser-issued setup-token for a full OAuth credentials blob that
// includes a separate sk-ant-ort01-... refreshToken. Empirically (test
// 2026-05-11, claude 2.1.138 inside the mindform image), when the
// accessToken's expiresAt is far enough in the future, claude never
// invokes the refresh path — so a placeholder refreshToken paired with
// a year-2099 expiresAt is sufficient to authenticate.
//
// One extra field on top of the Validate-required set is needed for
// claude's runtime credential check: the guard accepts the blob when
// either `scopes` or `subscriptionType` is non-empty. We provide
// `scopes: ["user:inference"]` — matching what claude writes for its
// `CLAUDE_CODE_OAUTH_TOKEN` env-var path — and deliberately omit
// `subscriptionType`. Without `scopes` the in-container claude 2.1.138
// rejects the blob with "Not logged in" even though Validate is
// satisfied (host claude 2.1.139 was more lenient and let this slip
// through pre-merge testing on v0.13.0). Setting `subscriptionType`
// to "max" without knowing the operator's actual tier would lie to
// claude's rate-limit display, so we leave it absent.
//
// Trade-off: if Anthropic revokes the setup-token server-side, the
// container's claude returns 401, the supervisor's exit classifier
// writes auth_required.json, and the operator re-runs `eidos forge
// login <name> --setup-token-stdin` (the same as any other auth
// recovery). The placeholder refreshToken never bridges revocation —
// it would not in the OAuth-exchange path either, since revocation
// invalidates the refresh token alongside the access token. Operators
// who want a real refresh-capable credentials.json should use
// `--generate` (interactive `claude setup-token`) instead.
package claudeauth

import (
	"encoding/json"
	"errors"
	"strings"
)

const (
	// setupTokenPlaceholderRefresh is the refreshToken stored when
	// wrapping a raw setup-token. Validate requires a non-empty
	// string; this value documents intent on inspection.
	setupTokenPlaceholderRefresh = "placeholder-no-refresh-from-setup-token"
	// setupTokenFarFutureExpiresAt is 2099-01-01 UTC in ms-since-epoch.
	// Picked far enough out that claude treats the credentials as
	// non-expiring and never tries to refresh.
	setupTokenFarFutureExpiresAt = int64(4070908800000)
	// setupTokenScope is the minimum OAuth scope claude accepts for a
	// setup-token-installed credentials blob. Matches the value claude
	// writes when reading the token from CLAUDE_CODE_OAUTH_TOKEN env.
	setupTokenScope = "user:inference"
)

// WrapSetupToken converts a raw setup-token string into a Validate-clean
// .credentials.json blob suitable for WriteToVolume. Whitespace around
// the token is trimmed. Returns an error only on empty input — token
// shape (prefix, length) is not validated because Anthropic may
// introduce new formats and the validation surface lives in claude itself.
func WrapSetupToken(token string) ([]byte, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("empty setup-token")
	}
	type oauth struct {
		AccessToken  string   `json:"accessToken"`
		RefreshToken string   `json:"refreshToken"`
		ExpiresAt    int64    `json:"expiresAt"`
		Scopes       []string `json:"scopes"`
	}
	type creds struct {
		ClaudeAiOauth oauth `json:"claudeAiOauth"`
	}
	return json.MarshalIndent(creds{ClaudeAiOauth: oauth{
		AccessToken:  token,
		RefreshToken: setupTokenPlaceholderRefresh,
		ExpiresAt:    setupTokenFarFutureExpiresAt,
		Scopes:       []string{setupTokenScope},
	}}, "", "  ")
}
