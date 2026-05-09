package ipc

import "encoding/json"

type Request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements the standard error interface so *Error can be returned
// from Go-level call sites (e.g. (*daemon.Daemon).Call) and recovered via
// errors.As to preserve the typed code.
func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

const (
	ErrInvalidRequest        = "INVALID_REQUEST"
	ErrInvalidParams         = "INVALID_PARAMS"
	ErrUnknownMethod         = "UNKNOWN_METHOD"
	ErrNotInitialized        = "NOT_INITIALIZED"
	ErrContactNotFound       = "CONTACT_NOT_FOUND"
	ErrContactExists         = "CONTACT_EXISTS"
	ErrInvalidNpub           = "INVALID_NPUB"
	ErrRelayUnreachable      = "RELAY_UNREACHABLE"
	ErrRelayRejected         = "RELAY_REJECTED"
	ErrNoRelaysReachable     = "NO_RELAYS_REACHABLE"
	ErrInternal              = "INTERNAL"
	ErrLabelAmbiguous        = "LABEL_AMBIGUOUS"
	ErrInviteInvalidToken    = "INVITE_INVALID_TOKEN"
	ErrInviteExpired         = "INVITE_EXPIRED"
	ErrInviteExhausted       = "INVITE_EXHAUSTED"
	ErrInviteRevoked         = "INVITE_REVOKED"
	ErrInviteAlreadyRedeemed = "INVITE_ALREADY_REDEEMED"
	ErrInvitePrefixAmbiguous = "INVITE_PREFIX_AMBIGUOUS"
)
