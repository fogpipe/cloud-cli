package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// --load prints what the edge served under the load lines: the requests by
// class with the 5xx codes apart, bytes each way, then app time and edge time
// at p50, p95 and p99, a latency past the top bucket as above it.
func TestHTTPLinesRenderRequestsThenLatency(t *testing.T) {
	h := &client.HTTPLoad{
		Window: "1h", Step: "1m", Covered: "1h",
		Requests: &client.HTTPStatusCounts{Total: 1204, C2xx: 1180, C4xx: 20, C5xx: 4, S500: 1, S502: 3},
		AppTime:  &client.HTTPLatency{P50: client.HTTPQuantile{Ms: 12.5}, P95: client.HTTPQuantile{Ms: 80}, P99: client.HTTPQuantile{Ms: 10000, Above: true}},
		EdgeTime: &client.HTTPLatency{P50: client.HTTPQuantile{Ms: 15}, P95: client.HTTPQuantile{Ms: 1500}, P99: client.HTTPQuantile{Ms: 2400}},
		Bytes:    &client.HTTPBytes{Request: 2 << 20, Response: 30 << 20},
	}
	assert.Equal(t, []string{
		"http    1204 requests: 2xx 1180, 4xx 20, 5xx 4 (500 1, 502 3) · in 2Mi, out 30Mi",
		"latency app p50 12.5ms p95 80ms p99 >10s · edge p50 15ms p95 1.5s p99 2.4s",
	}, httpLines(h, nil))

	week := &client.HTTPLoad{Window: "1w", Step: "1h", Covered: "3d11h",
		Requests: &client.HTTPStatusCounts{Total: 1, C5xx: 1}, Bytes: &client.HTTPBytes{}}
	assert.Equal(t, []string{
		"http    1 request: 5xx 1 (other 1) · in 0B, out 0B   (covered 3d11h of 1w, 1h steps)",
	}, httpLines(week, nil), "an untimed request has no latency line")
}

// No requests over a covered window is measured; nothing recorded is not; an
// app the edge does not serve says why; a source that did not answer says so;
// and a document read without a window prints nothing.
func TestHTTPLinesSeparateQuietFromUnknown(t *testing.T) {
	assert.Equal(t, []string{"http    no requests over 6h"},
		httpLines(&client.HTTPLoad{Window: "6h", Step: "1m", Covered: "6h", Requests: &client.HTTPStatusCounts{}}, nil))
	assert.Equal(t, []string{"http    nothing recorded over 1w"},
		httpLines(&client.HTTPLoad{Window: "1w", Step: "1h", Covered: "0s", Requests: &client.HTTPStatusCounts{}}, nil))
	assert.Equal(t, []string{"http    not measured: a worker app is not served through the edge"},
		httpLines(&client.HTTPLoad{Window: "1h", Step: "1m", Unmeasured: "worker"}, nil))
	assert.Equal(t, []string{"http    not read — prometheus unreachable"},
		httpLines(nil, []client.UncheckedStatus{{Check: "http", Error: "prometheus unreachable"}}))
	assert.Nil(t, httpLines(nil, nil))
}

// --series draws each step's requests against the busiest; a step the source
// held nothing for is "·".
func TestHTTPLinesDrawTheSeries(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	point := func(i int, covered bool, n int64) client.HTTPPoint {
		p := client.HTTPPoint{Start: at.Add(time.Duration(i) * 15 * time.Minute), Covered: covered}
		if covered {
			p.Requests = &client.HTTPStatusCounts{Total: n, C2xx: n}
		}
		return p
	}
	h := &client.HTTPLoad{Window: "1h", Step: "1m", Covered: "45m",
		Requests:   &client.HTTPStatusCounts{Total: 80, C2xx: 80},
		SeriesStep: "15m",
		Series:     []client.HTTPPoint{point(0, false, 0), point(1, true, 0), point(2, true, 20), point(3, true, 60)},
	}
	lines := httpLines(h, nil)
	assert.Equal(t, "http    ·▁▃█  requests per 15m, against the busiest (60)", lines[len(lines)-1])
}

// The lines sit under the app and under a website's bucket; a document read
// without a load window carries neither, and a bucket serving no website is
// never said to be unread.
func TestProjectStatusRendersHTTPUnderAppsAndWebsites(t *testing.T) {
	s := &client.ProjectStatus{
		Project: client.StatusProject{Name: "demo", Namespace: "acme-demo", Egress: "open"},
		Apps: []client.AppStatus{{Name: "api", Status: "running", Desired: 1, Ready: 1,
			Load: &client.Load{Window: "1h", Step: "1m", Covered: "1h"},
			HTTP: &client.HTTPLoad{Window: "1h", Step: "1m", Covered: "1h", Requests: &client.HTTPStatusCounts{Total: 3, C2xx: 3}},
		}},
		Buckets: []client.BucketStatus{
			{Name: "docs", Status: "ready", WebsiteEnabled: true, URL: "https://docs.demo.se"},
			{Name: "uploads", Status: "ready"},
		},
		Unchecked: []client.UncheckedStatus{{Check: "http", Error: "the record is unreadable"}},
	}
	out := renderProjectStatus(s, nil, false)
	assert.Contains(t, out, "http    3 requests: 2xx 3")
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, "docs") && strings.Contains(l, "docs.demo.se") {
			assert.Contains(t, lines[i+1], "http    not read — the record is unreadable")
		}
		if strings.Contains(l, "uploads") {
			assert.NotContains(t, lines[i+1], "http")
		}
	}

	s.Apps[0].Load, s.Apps[0].HTTP, s.Unchecked = nil, nil, nil
	assert.NotContains(t, renderProjectStatus(s, nil, false), "http ")
}
