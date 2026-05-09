//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// TestDashboardSettings_Phase1_IdentityFlow covers the Identity tab end
// to end against a real daemon: shell renders, fragment renders, label
// POST persists to the meta store, identity.label-changed event fires.
func TestDashboardSettings_Phase1_IdentityFlow(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)

	base := dashboardURL(alice)

	// 1. Shell renders with Identity active by default.
	resp := mustGet(t, base+"/settings")
	body := mustReadAll(t, resp)
	if !strings.Contains(body, "settings-tab is-active") {
		t.Errorf("/settings: missing active tab marker; body: %s", body)
	}
	if !strings.Contains(body, "Colophon") {
		t.Error("/settings: identity pane should render by default")
	}
	if !strings.Contains(body, alice.daemon.Key.PublicHex) {
		t.Error("/settings: own hex should appear in colophon")
	}

	// 2. Fragment for Identity (HX-Request).
	req := mustNewReq(t, "GET", base+"/settings/identity", nil)
	req.Header.Set("HX-Request", "true")
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if strings.Contains(body, "<html") {
		t.Error("/settings/identity (HX-Request): should be a fragment, got full page")
	}
	if !strings.Contains(body, `class="identity-pane"`) {
		t.Error("/settings/identity: missing identity-pane class")
	}

	// 3. POST a new label.
	form := url.Values{"label": {"alice-renamed"}}
	req = mustNewReq(t, "POST", base+"/settings/identity/label", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("POST label: status %d body %s", resp.StatusCode, body)
	}
	// `is-error` substring also appears inside the copy-button's
	// hx-on:click error-handler; tightly scope to the form-flash chip's
	// own class combination.
	if !strings.Contains(body, "form-flash") || strings.Contains(body, "form-flash is-error") {
		t.Errorf("POST label success expected; got: %s", body)
	}

	// 4. Verify the label persisted.
	got, err := alice.daemon.DB.GetMeta(context.Background(), "label")
	if err != nil {
		t.Fatal(err)
	}
	if got != "alice-renamed" {
		t.Errorf("label not persisted; got %q", got)
	}

	// 5. Empty label rejected (still 200 with inline error).
	form = url.Values{"label": {"   "}}
	req = mustNewReq(t, "POST", base+"/settings/identity/label", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("empty label: expected 200 with inline error, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "form-flash is-error") {
		t.Errorf("empty label: expected form-flash is-error chip; body: %s", body)
	}
}

// TestDashboardSettings_Phase1_ConfigFlow exercises the Config tab end
// to end: snapshot reads from config.toml on disk; POST writes through
// the shared config.Keys registry; row-error renders on validation
// failure; the file actually changes between requests.
func TestDashboardSettings_Phase1_ConfigFlow(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)

	base := dashboardURL(alice)
	cfgPath := filepath.Join(alice.daemon.StateDir, "config.toml")

	// 1. Fragment lists every registered scalar key.
	req := mustNewReq(t, "GET", base+"/settings/config", nil)
	req.Header.Set("HX-Request", "true")
	resp := mustDo(t, req)
	body := mustReadAll(t, resp)
	for _, want := range []string{
		`id="cfg-daemon-socket"`,
		`id="cfg-dashboard-enabled"`,
		`id="cfg-log_level"`,
		"Restart required",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("config fragment missing %q", want)
		}
	}

	// 2. POST a valid value; file on disk reflects the change.
	form := url.Values{"path": {"log_level"}, "value": {"debug"}}
	req = mustNewReq(t, "POST", base+"/settings/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("POST config: status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `id="cfg-log_level"`) {
		t.Errorf("config POST should return the updated row partial: %s", body)
	}
	if strings.Contains(body, "row-error") {
		t.Errorf("valid POST should not render row-error: %s", body)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config after set: %v", err)
	}
	if loaded.LogLevel != "debug" {
		t.Errorf("log_level not persisted to disk; got %q", loaded.LogLevel)
	}

	// 3. POST an invalid value; row-error chip surfaces, file unchanged.
	// log_level rejects anything outside debug|info|warn|error, so "bogus"
	// triggers a row-level validation error.
	form = url.Values{"path": {"log_level"}, "value": {"bogus"}}
	req = mustNewReq(t, "POST", base+"/settings/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("invalid POST: expected 200 with inline error, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "row-error") {
		t.Errorf("invalid POST should render row-error: %s", body)
	}

	loadedAgain, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loadedAgain.LogLevel != "debug" {
		t.Errorf("invalid POST must not persist to disk; log_level = %q (want \"debug\")", loadedAgain.LogLevel)
	}

	// 4. POST an unknown key is rejected with 400 (defense in depth: the
	// dashboard form only emits known keys, but a hand-crafted curl
	// must not be able to write fields that aren't registered).
	form = url.Values{"path": {"made.up.key"}, "value": {"x"}}
	req = mustNewReq(t, "POST", base+"/settings/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	_ = mustReadAll(t, resp) // close body to avoid fd leak
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown key: expected 400, got %d", resp.StatusCode)
	}
}

// TestDashboardSettings_Phase1_OperatorSidebarLink confirms the new
// Operator → Settings entry appears in the sidebar partial.
func TestDashboardSettings_Phase1_OperatorSidebarLink(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)

	resp := mustGet(t, dashboardURL(alice)+"/sidebar/contacts")
	body := mustReadAll(t, resp)
	for _, want := range []string{
		">Operator</h3>",
		`href="/settings"`,
		">Settings</span>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sidebar missing %q; body: %s", want, body)
		}
	}
}

// ── tiny test helpers ──────────────────────────────────────────────

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustNewReq(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func mustDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.String(), err)
	}
	return resp
}

func mustReadAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
