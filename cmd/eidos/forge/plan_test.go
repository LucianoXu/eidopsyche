package forge

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPlanAddInDuration(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	plan, msg, err := planAdd(dir, now, "follow up on bob", "2h", "")
	if err != nil {
		t.Fatalf("planAdd: %v", err)
	}
	if plan.Hint != "follow up on bob" {
		t.Errorf("Hint = %q", plan.Hint)
	}
	if plan.At != now.Add(2*time.Hour).Unix() {
		t.Errorf("At = %d", plan.At)
	}
	if !strings.Contains(msg, plan.ID) {
		t.Errorf("msg %q lacks plan id", msg)
	}
}

func TestPlanAddAtRFC3339(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	at := now.Add(3 * time.Hour).Format(time.RFC3339)
	plan, _, err := planAdd(dir, now, "x", "", at)
	if err != nil {
		t.Fatalf("planAdd: %v", err)
	}
	if plan.At != now.Add(3*time.Hour).Unix() {
		t.Errorf("At = %d", plan.At)
	}
}

func TestPlanAddAtUnixSeconds(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	at := now.Add(3 * time.Hour).Unix()
	plan, _, err := planAdd(dir, now, "x", "", strconv.FormatInt(at, 10))
	if err != nil {
		t.Fatalf("planAdd: %v", err)
	}
	if plan.At != at {
		t.Errorf("At = %d", plan.At)
	}
}

func TestPlanAddRejectsBothInAndAt(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_, _, err := planAdd(dir, now, "x", "2h", "1715284800")
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected exactly-one error, got %v", err)
	}
}

func TestPlanAddRejectsNeither(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_, _, err := planAdd(dir, now, "x", "", "")
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected exactly-one error, got %v", err)
	}
}

func TestPlanAddRejectsBadAt(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_, _, err := planAdd(dir, now, "x", "", "not-a-time")
	if err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Errorf("expected RFC3339 hint in error, got %v", err)
	}
}

func TestPlanListFormat(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_, _, _ = planAdd(dir, now, "first", "2h", "")
	_, _, _ = planAdd(dir, now.Add(time.Second), "second", "4h", "")
	out, err := planList(dir, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("plans missing from output:\n%s", out)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "AT") || !strings.Contains(out, "HINT") {
		t.Errorf("header missing:\n%s", out)
	}
	if strings.Index(out, "first") > strings.Index(out, "second") {
		t.Errorf("not in id order:\n%s", out)
	}
}

func TestPlanListEmpty(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	out, err := planList(dir, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no plans") {
		t.Errorf("empty list should say so, got %q", out)
	}
}

func TestPlanCancelByID(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	plan, _, _ := planAdd(dir, now, "x", "2h", "")
	if err := planCancel(dir, plan.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	out, _ := planList(dir, now, time.UTC)
	if !strings.Contains(out, "no plans") {
		t.Errorf("plan still present after cancel:\n%s", out)
	}
}

func TestPlanClearReportsCount(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_, _, _ = planAdd(dir, now, "a", "2h", "")
	_, _, _ = planAdd(dir, now.Add(time.Second), "b", "3h", "")
	msg, err := planClear(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "2") {
		t.Errorf("clear count missing: %q", msg)
	}
}

func TestPlanClearEmpty(t *testing.T) {
	dir := t.TempDir()
	msg, err := planClear(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "no plans") {
		t.Errorf("empty clear message: %q", msg)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "now"},
		{30 * time.Second, "30s"},
		{2 * time.Minute, "2m0s"},
		{2*time.Minute + 30*time.Second, "2m30s"},
		{1*time.Hour + 57*time.Minute, "1h57m"},
		{24*time.Hour + 8*time.Hour, "1d8h"},
	}
	for _, c := range cases {
		got := formatDuration(c.in)
		if got != c.want {
			t.Errorf("formatDuration(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNewPlanInContainerCmdHasSubcommands(t *testing.T) {
	cmd := newPlanInContainerCmd()
	want := []string{"add", "list", "cancel", "clear"}
	got := map[string]bool{}
	for _, sub := range cmd.Commands() {
		got[sub.Name()] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing subcommand %q", w)
		}
	}
}

func TestNewPlanHostCmdHasOnlyListAndCancel(t *testing.T) {
	cmd := newPlanHostCmd()
	got := map[string]bool{}
	for _, sub := range cmd.Commands() {
		got[sub.Name()] = true
	}
	if !got["list"] || !got["cancel"] {
		t.Errorf("host plan missing list/cancel: %v", got)
	}
	if got["add"] || got["clear"] {
		t.Errorf("host plan should not expose add/clear: %v", got)
	}
}
