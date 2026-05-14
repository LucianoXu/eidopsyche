package mcp

import (
	"encoding/json"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// fakeIPC is a test double for the rawIPC interface (see client.go). It
// records the most-recent Call's (method, params), and returns the
// canned (result, errResp, transportErr) tuple. Used by every
// tools_*_test.go in this package.
type fakeIPC struct {
	lastMethod   string
	lastParams   any
	result       any
	errResp      *ipc.Error
	transportErr error
}

func (f *fakeIPC) Call(method string, params any, result any) (*ipc.Error, error) {
	f.lastMethod = method
	f.lastParams = params
	if f.transportErr != nil {
		return nil, f.transportErr
	}
	if f.errResp != nil {
		return f.errResp, nil
	}
	if f.result != nil && result != nil {
		b, _ := json.Marshal(f.result)
		_ = json.Unmarshal(b, result)
	}
	return nil, nil
}

func (f *fakeIPC) Close() error { return nil }
