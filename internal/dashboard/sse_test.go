package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

// fakeDepsWithChan exposes a channel under a stable cancel; tests push
// events through it.
type fakeDepsWithChan struct {
	fakeDeps
	ch chan Event
}

func (f *fakeDepsWithChan) SubscribeEvents() (<-chan Event, func()) {
	if f.ch == nil {
		f.ch = make(chan Event, 4)
	}
	return f.ch, func() {}
}

func (f *fakeDepsWithChan) push(e Event) { f.ch <- e }

func TestSSE_DeliversInboxMessage(t *testing.T) {
	deps := &fakeDepsWithChan{}
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	hub := newSSEHub(deps)
	srv := httptest.NewServer(hub.handler(r, slogDiscard()))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("content-type: %s", resp.Header.Get("Content-Type"))
	}

	// Wait briefly for the handler to subscribe before pushing.
	time.Sleep(100 * time.Millisecond)
	deps.push(Event{
		Kind: "inbox.message",
		Message: &inbox.Message{
			From:       "p1",
			Content:    `{"v":1,"type":"chat","text":"hi"}`,
			ReceivedAt: time.Now().Unix(),
		},
	})

	buf := make([]byte, 4096)
	deadline := time.Now().Add(1 * time.Second)
	var seen string
	for time.Now().Before(deadline) {
		n, _ := resp.Body.Read(buf)
		seen += string(buf[:n])
		// Event names are now counterpart-scoped: "inbox.message:<pk>"
		if strings.Contains(seen, "event: inbox.message:p1") {
			return
		}
		if n == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatalf("did not observe SSE event in stream: %q", seen)
}
