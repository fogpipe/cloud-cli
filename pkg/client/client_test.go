package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientCreateProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/projects", r.URL.Path)

		var req CreateProjectRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "my-project", req.Name)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Project{
			ID:   "proj-1",
			Name: "my-project",
		})
	}))
	defer server.Close()

	c := New(server.URL, "test-key")
	project, err := c.CreateProject(context.Background(), CreateProjectRequest{
		Name: "my-project",
	})

	require.NoError(t, err)
	assert.Equal(t, "proj-1", project.ID)
	assert.Equal(t, "my-project", project.Name)
}

func TestClientListWorkloadEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/projects/proj-1/workloads/web/events", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]WorkloadEvent{{
			Reason:  "FailedCreate",
			Message: "pods \"web-1\" is forbidden: exceeded quota",
			Source:  "event",
			Object:  "web-6f9",
			Count:   3,
		}})
	}))
	defer server.Close()

	c := New(server.URL, "test-key")
	events, err := c.ListWorkloadEvents(context.Background(), "proj-1", "web")

	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "FailedCreate", events[0].Reason)
	assert.Equal(t, int32(3), events[0].Count)
}

func TestClientErrorHandling_NestedFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"code":    "INVALID_REQUEST",
				"message": "name is required",
			},
		})
	}))
	defer server.Close()

	c := New(server.URL, "test-key")
	_, err := c.ListProjects(context.Background())

	require.Error(t, err)
	apiErr, ok := err.(*APIError)
	require.True(t, ok, "error should be *APIError")
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, "INVALID_REQUEST", apiErr.Code)
	assert.Equal(t, "name is required", apiErr.Message)
}

func TestClientErrorHandling_FlatFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "project not found",
		})
	}))
	defer server.Close()

	c := New(server.URL, "test-key")
	_, err := c.GetProject(context.Background(), "proj-unknown")

	require.Error(t, err)
	apiErr, ok := err.(*APIError)
	require.True(t, ok, "error should be *APIError")
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
	assert.Equal(t, "project not found", apiErr.Message)
}

func TestClientErrorHandling_MessageFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "access denied",
		})
	}))
	defer server.Close()

	c := New(server.URL, "test-key")
	_, err := c.ListProjects(context.Background())

	require.Error(t, err)
	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, "access denied", apiErr.Message)
}

func TestClientErrorHandling_NonJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	c := New(server.URL, "test-key")
	_, err := c.ListProjects(context.Background())

	require.Error(t, err)
	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "500")
}

func TestClientAuthHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer my-secret-key", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Project{})
	}))
	defer server.Close()

	c := New(server.URL, "my-secret-key")
	_, err := c.ListProjects(context.Background())
	require.NoError(t, err)
}

func TestClientNoAuthHeader_WhenEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Project{})
	}))
	defer server.Close()

	c := New(server.URL, "")
	_, err := c.ListProjects(context.Background())
	require.NoError(t, err)
}

func TestClientListApps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/projects/proj-1/apps", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]*App{
			{ID: "app-1", Name: "web"},
			{ID: "app-2", Name: "worker"},
		})
	}))
	defer server.Close()

	c := New(server.URL, "key")
	apps, err := c.ListApps(context.Background(), "proj-1")
	require.NoError(t, err)
	assert.Len(t, apps, 2)
	assert.Equal(t, "web", apps[0].Name)
}

func TestClientDeleteProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/v1/projects/proj-1", r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(Project{ID: "proj-1", Name: "demo", Status: "deleting"})
	}))
	defer server.Close()

	c := New(server.URL, "key")
	project, err := c.DeleteProject(context.Background(), "proj-1")
	require.NoError(t, err)
	assert.Equal(t, "deleting", project.Status, "the delete is accepted, not finished")
}

// The teardown outlives the request, so "deleted" is something a caller observes
// rather than something the delete call returns (#865).
func TestWaitProjectDeletedReturnsWhenTheProjectIsGone(t *testing.T) {
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if reads < 2 {
			_ = json.NewEncoder(w).Encode(Project{ID: "proj-1", Status: "deleting"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "project not found"}})
	}))
	defer server.Close()

	c := New(server.URL, "key")
	require.NoError(t, c.WaitProjectDeleted(context.Background(), "proj-1", time.Millisecond))
	assert.Equal(t, 2, reads, "it keeps reading while the project is still there")
}

// The teardown takes the project's IAM bindings with it, so the read that used
// to answer 200 answers 403 rather than 404 — which is the same news.
func TestWaitProjectDeletedTreatsALostBindingAsGone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
	}))
	defer server.Close()

	c := New(server.URL, "key")
	require.NoError(t, c.WaitProjectDeleted(context.Background(), "proj-1", time.Millisecond))
}

func TestAPIError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      APIError
		expected string
	}{
		{
			name:     "with message",
			err:      APIError{StatusCode: 400, Code: "INVALID", Message: "bad request"},
			expected: "bad request",
		},
		{
			name:     "with code only",
			err:      APIError{StatusCode: 400, Code: "INVALID"},
			expected: "INVALID",
		},
		{
			name:     "with status only",
			err:      APIError{StatusCode: 500},
			expected: "HTTP 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

func TestAPIError_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCode    string
		wantMessage string
		wantErr     bool
	}{
		{
			name:        "nested format",
			input:       `{"error":{"code":"NOT_FOUND","message":"not found"}}`,
			wantCode:    "NOT_FOUND",
			wantMessage: "not found",
		},
		{
			name:        "flat format",
			input:       `{"error":"something went wrong"}`,
			wantMessage: "something went wrong",
		},
		{
			name:        "message format",
			input:       `{"message":"access denied"}`,
			wantMessage: "access denied",
		},
		{
			name:    "unknown format",
			input:   `{"foo":"bar"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var apiErr APIError
			err := json.Unmarshal([]byte(tt.input), &apiErr)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCode, apiErr.Code)
			assert.Equal(t, tt.wantMessage, apiErr.Message)
		})
	}
}

func TestClientGetAppLogs_Query(t *testing.T) {
	cases := []struct {
		name string
		req  LogsRequest
		want string
	}{
		{"default", LogsRequest{}, ""},
		{"follow", LogsRequest{Follow: true}, "follow=true"},
		{"tail", LogsRequest{Tail: 500}, "tail=500"},
		{"both", LogsRequest{Follow: true, Tail: 500}, "follow=true&tail=500"},
		{"window", LogsRequest{Since: "24h", Until: "1h"}, "since=24h&until=1h"},
		{"absolute window", LogsRequest{Since: "2026-08-20T00:00:00Z"}, "since=2026-08-20T00%3A00%3A00Z"},
		{"timestamps", LogsRequest{Timestamps: true}, "timestamps=true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/apps/app-1/logs", r.URL.Path)
				assert.Equal(t, tc.want, r.URL.RawQuery)
				_, _ = w.Write([]byte("line\n"))
			}))
			defer server.Close()

			body, err := New(server.URL, "test-key").GetAppLogs(context.Background(), "app-1", tc.req)
			require.NoError(t, err)
			defer body.Close()
			out, err := io.ReadAll(body)
			require.NoError(t, err)
			assert.Equal(t, "line\n", string(out))
		})
	}
}

// A streamed response must outlive the bound that used to cut it. `app logs
// --follow` asks for thirty minutes (ADR-086) and got thirty seconds, because
// http.Client.Timeout covers reading the body and so cannot tell a long answer
// from a stuck one. The phase bounds that replaced it each answer a question
// that can be asked before the body exists.
func TestClientDoesNotBoundTheWholeExchange(t *testing.T) {
	c := New("http://example.invalid", "k")

	require.Zero(t, c.HTTPClient.Timeout,
		"a total deadline covers reading the body, so it bounds a stream by how long it is asked to run")

	transport, ok := c.HTTPClient.Transport.(*http.Transport)
	require.True(t, ok, "the client carries its own transport, so the phase bounds are its own")
	assert.Equal(t, responseHeaderTimeout, transport.ResponseHeaderTimeout,
		"a server that never answers is still bounded")
	assert.Equal(t, tlsHandshakeTimeout, transport.TLSHandshakeTimeout)
}

// The caller's context is what ends a follow, now that nothing shorter does.
func TestClientFollowEndsWithTheCallersContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for {
			if _, err := io.WriteString(w, "line\n"); err != nil {
				return
			}
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	body, err := New(server.URL, "k").GetAppLogs(ctx, "app", LogsRequest{Follow: true})
	require.NoError(t, err)
	defer body.Close()

	start := time.Now()
	_, err = io.Copy(io.Discard, body)
	assert.Error(t, err, "the stream ends because the context did, not because it succeeded")
	assert.Less(t, time.Since(start), 5*time.Second,
		"the context's deadline is what stops it")
}

// A rate-limited request is retried, and the retry carries the body again.
//
// The platform refuses before the handler runs, so nothing happened and there is
// no half-created resource for a repeat to collide with — which is what makes
// retrying a POST correct here rather than a hack.
func TestClient_RetriesARefusedRequestIncludingItsBody(t *testing.T) {
	bodies := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		if len(bodies) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"proj-1"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "fp-test")
	req, err := c.newRequest(context.Background(), http.MethodPost, "/api/v1/projects", map[string]string{"name": "demo"})
	require.NoError(t, err)

	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, c.do(req, &out))
	assert.Equal(t, "proj-1", out.ID)
	assert.Equal(t, []string{`{"name":"demo"}`, `{"name":"demo"}`, `{"name":"demo"}`}, bodies,
		"every attempt must carry the body, not just the first")
}

// Retrying is bounded: a caller that keeps retrying into a budget it has really
// exhausted is the load it is being refused for. The last refusal is what the
// caller is told about.
func TestClient_StopsRetryingAndReportsTheRefusal(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"too many requests, please retry later"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "fp-test")
	req, err := c.newRequest(context.Background(), http.MethodGet, "/api/v1/projects", nil)
	require.NoError(t, err)

	err = c.do(req, nil)
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	assert.Equal(t, maxRateLimitRetries+1, attempts)
}

// A retry never outlives the deadline the caller set — the caller's context is
// the bound, not the server's Retry-After (ADR-107).
func TestClient_RetryDoesNotOutliveTheCallersContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	c := New(srv.URL, "fp-test")
	req, err := c.newRequest(ctx, http.MethodGet, "/api/v1/projects", nil)
	require.NoError(t, err)

	start := time.Now()
	err = c.do(req, nil)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second,
		"the wait must end with the context, not run the server's full Retry-After")
}

// The wait is spread, because every client of one organization shares one budget
// and is therefore refused together and told the same second to return.
func TestRetryAfter_SpreadsTheAnswerWithoutComingBackEarly(t *testing.T) {
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		d := retryAfter("4")
		assert.GreaterOrEqual(t, d, 4*time.Second, "jitter is added, never subtracted")
		assert.LessOrEqual(t, d, 6*time.Second)
		seen[d] = true
	}
	assert.Greater(t, len(seen), 1, "every client waiting the same time recreates the burst")
}

// An unreadable or absent Retry-After is still a refusal: retry shortly rather
// than not at all.
func TestRetryAfter_FallsBackWhenTheHeaderIsUnusable(t *testing.T) {
	for _, header := range []string{"", "soon", "-3", "Wed, 21 Oct 2015 07:28:00 GMT"} {
		d := retryAfter(header)
		assert.GreaterOrEqual(t, d, time.Second, "header %q", header)
		assert.LessOrEqual(t, d, 2*time.Second, "header %q", header)
	}
}

// However long the server asks for, one refusal parks the caller for a bounded
// time.
func TestRetryAfter_IsCapped(t *testing.T) {
	assert.LessOrEqual(t, retryAfter("86400"), maxRetryAfter+maxRetryAfter/2)
}
