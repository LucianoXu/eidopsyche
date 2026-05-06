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

const (
	ErrInvalidRequest    = "INVALID_REQUEST"
	ErrInvalidParams     = "INVALID_PARAMS"
	ErrUnknownMethod     = "UNKNOWN_METHOD"
	ErrNotInitialized    = "NOT_INITIALIZED"
	ErrContactNotFound   = "CONTACT_NOT_FOUND"
	ErrContactExists     = "CONTACT_EXISTS"
	ErrInvalidNpub       = "INVALID_NPUB"
	ErrRelayUnreachable  = "RELAY_UNREACHABLE"
	ErrRelayRejected     = "RELAY_REJECTED"
	ErrNoRelaysReachable = "NO_RELAYS_REACHABLE"
	ErrInternal          = "INTERNAL"
)
