package main

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestMergeDepTargets(t *testing.T) {
	owners := []ownerRepo{
		{owner: "amarillier", repo: "Beta"},
		{owner: "amarillier", repo: "Alpha"},
		{owner: "amarillier", repo: "Gamma"}, // enumerated-only (not cloned)
	}
	local := []depTarget{
		{host: "github.com", owner: "amarillier", repo: "alpha", repoRoot: "/src/alpha"}, // overlaps Alpha
		{host: "github.com", owner: "someoneelse", repo: "Delta", repoRoot: "/src/delta"}, // local-only
		// Same owner/repo on a GHES host must NOT dedup with the github.com one.
		{host: "git.corp.tanium.com", owner: "amarillier", repo: "Beta", repoRoot: "/src/beta-internal"},
	}

	got := mergeDepTargets(owners, local)

	want := []depTarget{
		// github.com/* sort before git.corp.tanium.com? key is host/owner/repo
		// lowercased: "git.corp.tanium.com/..." < "github.com/..." alphabetically.
		{host: "git.corp.tanium.com", owner: "amarillier", repo: "Beta", repoRoot: "/src/beta-internal"},
		{host: "github.com", owner: "amarillier", repo: "alpha", repoRoot: "/src/alpha"}, // local wins → repoRoot kept
		{host: "github.com", owner: "amarillier", repo: "Beta"},                          // enumerated (distinct host from GHES Beta)
		{host: "github.com", owner: "amarillier", repo: "Gamma"},
		{host: "github.com", owner: "someoneelse", repo: "Delta", repoRoot: "/src/delta"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeDepTargets mismatch:\n got:  %+v\n want: %+v", got, want)
	}
}

func TestDueForDailyScan(t *testing.T) {
	// Reference "now": 2026-05-30 10:00 local.
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.Local)
	today := "2026-05-30"
	yesterday := "2026-05-29"

	cases := []struct {
		name    string
		enabled bool
		timeStr string
		lastRun string
		now     time.Time
		want    bool
	}{
		{"disabled", false, "09:00", "", now, false},
		{"bad time", true, "9am", yesterday, now, false},
		{"already ran today", true, "09:00", today, now, false},
		{"before slot", true, "11:30", yesterday, now, false},
		{"at/after slot, not run today", true, "09:00", yesterday, now, true},
		{"never run, after slot", true, "08:00", "", now, true},
		{"exactly at slot", true, "10:00", yesterday, now, true},
		{"midnight rollover (ran yesterday, slot passed)", true, "00:30", yesterday, now, true},
	}
	for _, c := range cases {
		if got := dueForDailyScan(c.enabled, c.timeStr, c.lastRun, c.now); got != c.want {
			t.Errorf("%s: dueForDailyScan(%v,%q,%q) = %v, want %v", c.name, c.enabled, c.timeStr, c.lastRun, got, c.want)
		}
	}
}

func TestClassifyDepError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want depErrBucket
	}{
		{"403", fmt.Errorf(`gh api: exit status 1` + "\n" + `{"message":"You are not authorized to perform this operation.","status":"403"}`), depErrNotAuthorized},
		{"http403line", fmt.Errorf("gh: You are not authorized. (HTTP 403)"), depErrNotAuthorized},
		{"404", fmt.Errorf(`gh api: exit status 1` + "\n" + `{"message":"Not Found","status":"404"}`), depErrNotFound},
		{"disabled", fmt.Errorf("gh api: Dependabot alerts are disabled for this repository"), depErrDisabled},
		{"auth", fmt.Errorf("gh api: Bad credentials (401)"), depErrAuth},
		{"other", fmt.Errorf("some unexpected failure"), depErrOther},
		{"nil", nil, depErrOther},
	}
	for _, c := range cases {
		if got := classifyDepError(c.err); got != c.want {
			t.Errorf("%s: classifyDepError = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMergeDepTargetsEmpty(t *testing.T) {
	if got := mergeDepTargets(nil, nil); len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
	// Local-only.
	local := []depTarget{{owner: "o", repo: "r", repoRoot: "/p"}}
	got := mergeDepTargets(nil, local)
	if !reflect.DeepEqual(got, local) {
		t.Errorf("local-only mismatch: got %+v want %+v", got, local)
	}
}

func TestSeverityCounts(t *testing.T) {
	alerts := []dependabotAlert{
		{Severity: "critical"},
		{Severity: "high"},
		{Severity: "high"},
		{Severity: "moderate"}, // GitHub's wording for medium
		{Severity: "low"},
		{Severity: ""}, // unknown → "other"
	}
	got := severityCounts(alerts)
	want := "1 critical · 2 high · 1 moderate · 1 low · 1 other"
	if got != want {
		t.Errorf("severityCounts = %q, want %q", got, want)
	}
	if severityCounts(nil) != "" {
		t.Errorf("expected empty string for no alerts")
	}
}
