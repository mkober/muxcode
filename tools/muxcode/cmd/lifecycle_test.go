package cmd

import "testing"

func TestResolveShowLimit(t *testing.T) {
	cases := []struct {
		name     string
		limit    int
		limitSet bool
		all      bool
		since    int64
		want     int
	}{
		{"unscoped keeps the default", 0, false, false, 0, lifecycleShowDefaultLimit},
		{"since without --limit is unlimited", 0, false, false, 1700000000, 0},
		{"since with --limit holds the limit", 10, true, false, 1700000000, 10},
		{"explicit --limit unscoped", 20, true, false, 0, 20},
		{"--all lifts an explicit limit", 10, true, true, 0, 0},
	}
	for _, c := range cases {
		if got := resolveShowLimit(c.limit, c.limitSet, c.all, c.since); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestTruncationNotice(t *testing.T) {
	if got := truncationNotice(10, 60); got != "showing last 10 of 60" {
		t.Errorf("truncated: got %q", got)
	}
	if got := truncationNotice(50, 60); got != "showing last 50 of 60" {
		t.Errorf("unscoped truncated: got %q", got)
	}
	if got := truncationNotice(5, 5); got != "" {
		t.Errorf("within limit must print no notice, got %q", got)
	}
	if got := truncationNotice(0, 0); got != "" {
		t.Errorf("empty result must print no notice, got %q", got)
	}
}
