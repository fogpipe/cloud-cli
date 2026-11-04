package cli

import (
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

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

func TestBucketUsageCountsObjects(t *testing.T) {
	used, one, many := int64(21), int64(1), int64(4)
	for _, c := range []struct {
		name  string
		count *int64
		want  string
	}{
		{"no count from the store", nil, "21B"},
		{"one object", &one, "21B · 1 object"},
		{"several objects", &many, "21B · 4 objects"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := bucketUsage(&client.Bucket{UsedBytes: &used, ObjectCount: c.count}); got != c.want {
				t.Errorf("bucketUsage = %q, want %q", got, c.want)
			}
		})
	}
}
