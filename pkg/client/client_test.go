package client

import (
	"context"
	"encoding/json"
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
