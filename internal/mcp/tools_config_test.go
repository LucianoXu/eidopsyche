package mcp

import (
	"context"
	"testing"
)

func TestEidosConfigSet_Routes(t *testing.T) {
	fake := &fakeIPC{}
	s := &Server{client: &Client{raw: fake}}
	_, _, err := s.eidosConfigSet(context.Background(), nil, ConfigSetInput{Path: "log_level", Value: "debug"})
	if err != nil {
		t.Fatalf("eidosConfigSet: %v", err)
	}
	if fake.lastMethod != "config.set" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
	p := fake.lastParams.(ConfigSetInput)
	if p.Path != "log_level" || p.Value != "debug" {
		t.Fatalf("params = %+v", p)
	}
}

func TestEidosConfigSet_ContextMismatchPropagates(t *testing.T) {
	fake := &fakeIPC{errResp: pathNotFound("mindform.model")}
	fake.errResp.Code = "CONTEXT_MISMATCH"
	fake.errResp.Message = "mindform.model requires container context"
	s := &Server{client: &Client{raw: fake}}
	_, _, err := s.eidosConfigSet(context.Background(), nil, ConfigSetInput{Path: "mindform.model", Value: "opus"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "[CONTEXT_MISMATCH] mindform.model requires container context" {
		t.Fatalf("err = %q", got)
	}
}
