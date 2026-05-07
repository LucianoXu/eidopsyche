//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/envelope"
)

// dashboardURL returns the running daemon's dashboard URL.
func dashboardURL(in *instance) string {
	return "http://" + in.daemon.Cfg.Dashboard.Listen
}

func TestDashboard_ShellOK(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)

	resp, err := http.Get(dashboardURL(alice) + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "alice") {
		t.Errorf("shell missing alice label; got: %s", string(body))
	}
	if !strings.Contains(string(body), "htmx") {
		t.Errorf("shell missing htmx script include")
	}
}

func TestDashboard_PostSendThenThreadShowsBubble(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)
	time.Sleep(300 * time.Millisecond)

	form := strings.NewReader("text=hello+from+web")
	req, _ := http.NewRequest("POST", dashboardURL(alice)+"/thread/"+bob.daemon.Key.PublicHex+"/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("send status %d body %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "hello from web") {
		t.Errorf("send response missing bubble: %s", string(body))
	}

	// Bob's thread for Alice should include the message.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(dashboardURL(bob) + "/thread/" + alice.daemon.Key.PublicHex)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "hello from web") {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("Bob's thread for Alice never showed the message")
}

func TestDashboard_SoftRejectVisibleInMalformedView(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)

	sendRawContent(t, bob, alice, "not an envelope")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(dashboardURL(alice) + "/messages?malformed=1")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "not_envelope") {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("Alice's malformed view never showed the soft-reject")
}

func TestDashboard_SSEDeliversInboxOnPeerSend(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)
	time.Sleep(300 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", dashboardURL(alice)+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Trigger a peer send a moment after subscribing.
	go func() {
		time.Sleep(400 * time.Millisecond)
		env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "via sse"}
		sendEnvelopeIPC(t, bob, alice.daemon.Key.PublicHex, env)
	}()

	buf := make([]byte, 8192)
	deadline := time.Now().Add(6 * time.Second)
	var seen string
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		seen += string(buf[:n])
		if strings.Contains(seen, "event: inbox.message") {
			return
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("did not observe inbox.message SSE event; saw: %q", seen)
}
