package cli

import "testing"

func TestStoredBytes(t *testing.T) {
	zero, some := int64(0), int64(1536)
	for _, c := range []struct {
		name string
		used *int64
		want string
	}{
		{"the store did not answer, which is not an empty bucket", nil, "—"},
		{"an empty bucket", &zero, "0B"},
		{"a bucket holding something", &some, "1.5Ki"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := storedBytes(c.used); got != c.want {
				t.Errorf("storedBytes = %q, want %q", got, c.want)
			}
		})
	}
}
