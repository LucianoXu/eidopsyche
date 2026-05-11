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
	ErrLifecycleBusy         = "LIFECYCLE_BUSY"
	ErrCardInvalid           = "CARD_INVALID"
	ErrContextMismatch       = "CONTEXT_MISMATCH"
	ErrPathNotFound          = "PATH_NOT_FOUND"
	ErrInconsistent          = "STATE_INCONSISTENT"
)

// WrapInconsistent composes an apply-failure error with a rollback-
// failure error into a single STATE_INCONSISTENT IPC error. Used by
// daemon.Mutate when an apply hook fails AND the framework's rollback
// attempt also fails — the on-disk state is at the new value but the
// applied state may not be.
func WrapInconsistent(applyErr, rollbackErr error) *Error {
	return &Error{
		Code:    ErrInconsistent,
		Message: "apply failed (" + applyErr.Error() + ") AND rollback failed (" + rollbackErr.Error() + "); on-disk state may not match applied state; next daemon restart will reconcile",
	}
}
