package cli

import "testing"

func TestUsageAgainstQuota(t *testing.T) {
	i := func(n int64) *int64 { return &n }
	cases := []struct {
		name  string
		used  *int64
		quota int64
		want  string
	}{
		{"empty bucket", i(0), 10 << 30, "0B of 10Gi (0%)"},
		{"under one percent", i(948 << 10), 10 << 30, "948Ki of 10Gi (<1%)"},
		{"rounded", i(3 << 30), 10 << 30, "3Gi of 10Gi (30%)"},
		{"full", i(10 << 30), 10 << 30, "10Gi of 10Gi (100%)"},
		// A quota is always a size (ADR-129), so a zero is the server not
		// having reported one — never a bucket with no bound. Rendering the
		// two alike is what let an unbounded bucket sit in a listing that
		// looked fully bounded.
		{"quota not reported", i(5 << 20), 0, "5Mi of unknown"},
		{"store not answering", nil, 10 << 30, "— of 10Gi"},
	}
	for _, c := range cases {
		if got := usageAgainstQuota(c.used, c.quota); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
