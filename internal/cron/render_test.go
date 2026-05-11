package cron

import (
	"strings"
	"testing"
)

func TestRender_Default2h(t *testing.T) {
	body, err := Render("")
	if err != nil {
		t.Fatal(err)
	}
	want := "0 */2 * * * EIDOS_IN_CONTAINER=1 /usr/local/bin/eidos forge wake --reason heartbeat\n"
	if body != want {
		t.Errorf("got:\n%s\nwant:\n%s", body, want)
	}
}

func TestRender_OneMinute(t *testing.T) {
	body, err := Render("1m")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, "*/1 * * * * ") {
		t.Errorf("expected */1 prefix; got %q", body)
	}
	if !strings.Contains(body, "EIDOS_IN_CONTAINER=1") {
		t.Errorf("crontab missing EIDOS_IN_CONTAINER=1 inline env; got:\n%s", body)
	}
}

func TestRender_ThirtyMin(t *testing.T) {
	body, err := Render("30m")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, "*/30 * * * * ") {
		t.Errorf("expected */30 prefix; got %q", body)
	}
}

func TestRender_TwentyFourHour(t *testing.T) {
	body, err := Render("24h")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, "0 0 * * * ") {
		t.Errorf("expected 24h pattern; got %q", body)
	}
}

func TestRender_RejectsUnsupported(t *testing.T) {
	if _, err := Render("90m"); err == nil {
		t.Error("expected error for unsupported interval")
	}
}

func TestRender_RejectsGarbage(t *testing.T) {
	if _, err := Render("not-a-duration"); err == nil {
		t.Error("expected parse error")
	}
}

func TestRenderOrDefault_FallsBackOnError(t *testing.T) {
	body, _, err := RenderOrDefault("90m")
	if err == nil {
		t.Error("expected error")
	}
	if !strings.HasPrefix(body, "0 */2 * * * ") {
		t.Errorf("expected default 2h fallback body; got %q", body)
	}
}

func TestRenderOrDefault_PassesThroughOnSuccess(t *testing.T) {
	body, rendered, err := RenderOrDefault("5m")
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "5m" {
		t.Errorf("rendered = %q; want 5m", rendered)
	}
	if !strings.HasPrefix(body, "*/5 * * * * ") {
		t.Errorf("body = %q", body)
	}
}
