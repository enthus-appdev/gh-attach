package gh

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestPushAttachmentsCreatesBlob(t *testing.T) {
	var calls []string

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/42", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "GET ref")
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST blob")
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob-sha-1"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST tree")
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "tree-sha-1"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST commit")
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "commit-sha-1"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/refs", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST ref")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"ref": "refs/uploads/issues/42"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.png")
	if err := os.WriteFile(testFile, []byte("fake-png-data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := &Repo{Owner: "owner", Name: "repo"}
	client := &GitDataClient{BaseURL: srv.URL, Token: "test-token"}

	paths, commitSHA, err := client.PushAttachments(repo, "uploads/issues/42", "upload for #42", []string{testFile})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if commitSHA != "commit-sha-1" {
		t.Errorf("commitSHA = %q, want %q", commitSHA, "commit-sha-1")
	}
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	if paths[0].Path != "test.png" {
		t.Errorf("path = %q, want %q", paths[0].Path, "test.png")
	}

	expectedCalls := []string{"GET ref", "POST blob", "POST tree", "POST commit", "POST ref"}
	if len(calls) != len(expectedCalls) {
		t.Fatalf("API calls = %v, want %v", calls, expectedCalls)
	}
	for i, c := range calls {
		if c != expectedCalls[i] {
			t.Errorf("call[%d] = %q, want %q", i, c, expectedCalls[i])
		}
	}
}

func TestPushAttachmentsAppendsToExistingRef(t *testing.T) {
	var calls []string

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/42", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "GET ref")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": map[string]string{"sha": "existing-commit-sha"},
		})
	})

	mux.HandleFunc("GET /repos/owner/repo/git/commits/existing-commit-sha", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "GET commit")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tree": map[string]string{"sha": "existing-tree-sha"},
		})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST blob")
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob-sha-1"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST tree")
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["base_tree"] != "existing-tree-sha" {
			t.Errorf("base_tree = %v, want %q", body["base_tree"], "existing-tree-sha")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "new-tree-sha"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST commit")
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "new-commit-sha"})
	})

	mux.HandleFunc("PATCH /repos/owner/repo/git/refs/uploads/issues/42", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "PATCH ref")
		_ = json.NewEncoder(w).Encode(map[string]string{"ref": "refs/uploads/issues/42"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.png")
	if err := os.WriteFile(testFile, []byte("fake-png-data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := &Repo{Owner: "owner", Name: "repo"}
	client := &GitDataClient{BaseURL: srv.URL, Token: "test-token"}

	_, commitSHA, err := client.PushAttachments(repo, "uploads/issues/42", "upload for #42", []string{testFile})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commitSHA != "new-commit-sha" {
		t.Errorf("commitSHA = %q, want %q", commitSHA, "new-commit-sha")
	}

	expectedCalls := []string{"GET ref", "GET commit", "POST blob", "POST tree", "POST commit", "PATCH ref"}
	if len(calls) != len(expectedCalls) {
		t.Fatalf("API calls = %v, want %v", calls, expectedCalls)
	}
	for i, c := range calls {
		if c != expectedCalls[i] {
			t.Errorf("call[%d] = %q, want %q", i, c, expectedCalls[i])
		}
	}
}

// TestPushAttachmentsUsesMiscRefPath asserts that PushAttachments is
// namespace-agnostic: when called with an ad-hoc refPath like
// "uploads/misc/design-v2", it hits that exact ref on the GitHub API and
// uses the caller-supplied commit message. This is the core invariant
// that backs the --key ad-hoc upload mode without duplicating the push
// logic per namespace.
func TestPushAttachmentsUsesMiscRefPath(t *testing.T) {
	var postedRef string
	var postedCommitMessage string

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/misc/design-v2", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob-sha"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "tree-sha"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if msg, ok := body["message"].(string); ok {
			postedCommitMessage = msg
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "commit-sha"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/refs", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		postedRef = body["ref"]
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"ref": body["ref"]})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "mockup.png")
	if err := os.WriteFile(testFile, []byte("fake-png-data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := &Repo{Owner: "owner", Name: "repo"}
	client := &GitDataClient{BaseURL: srv.URL, Token: "test-token"}

	_, _, err := client.PushAttachments(repo, "uploads/misc/design-v2", "upload for misc/design-v2", []string{testFile})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if postedRef != "refs/uploads/misc/design-v2" {
		t.Errorf("created ref = %q, want %q", postedRef, "refs/uploads/misc/design-v2")
	}
	if postedCommitMessage != "upload for misc/design-v2" {
		t.Errorf("commit message = %q, want %q", postedCommitMessage, "upload for misc/design-v2")
	}
}

// TestPushAttachmentsRejectsBasenameCollision asserts the pre-flight check
// that two source files with the same basename in a single upload are
// rejected before any GitHub API calls. Without this check, the second
// file would silently overwrite the first in the tree.
func TestPushAttachmentsRejectsBasenameCollision(t *testing.T) {
	tmpDir := t.TempDir()
	dir1 := filepath.Join(tmpDir, "a")
	dir2 := filepath.Join(tmpDir, "b")
	if err := os.MkdirAll(dir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir2, 0755); err != nil {
		t.Fatal(err)
	}
	file1 := filepath.Join(dir1, "img.png")
	file2 := filepath.Join(dir2, "img.png")
	if err := os.WriteFile(file1, []byte("data1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("data2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use an unreachable BaseURL — if the collision check works, no HTTP call should be made.
	repo := &Repo{Owner: "owner", Name: "repo"}
	client := &GitDataClient{BaseURL: "http://127.0.0.1:1", Token: "test-token"}

	_, _, err := client.PushAttachments(repo, "uploads/issues/42", "upload for #42", []string{file1, file2})
	if err == nil {
		t.Fatal("expected error for basename collision, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate basename") {
		t.Errorf("error = %q, want substring %q", err.Error(), "duplicate basename")
	}
	if !strings.Contains(err.Error(), "img.png") {
		t.Errorf("error = %q, want it to mention the colliding basename", err.Error())
	}
}

// ---------------------------------------------------------------------
// NewGitDataClient — uses stubbed execCommand to avoid touching gh CLI.
// ---------------------------------------------------------------------

func TestNewGitDataClient(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		withStubExec(t, stubOutput("gho_token"))
		client, err := NewGitDataClient()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client.Token != "gho_token" {
			t.Errorf("token = %q, want gho_token", client.Token)
		}
		if client.BaseURL != "https://api.github.com" {
			t.Errorf("baseURL = %q, want https://api.github.com", client.BaseURL)
		}
	})

	t.Run("gh auth token fails", func(t *testing.T) {
		withStubExec(t, stubFail())
		_, err := NewGitDataClient()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// ---------------------------------------------------------------------
// HTTP helper error paths (get/post/postNoResponse/patch return non-2xx
// or network failures).
// ---------------------------------------------------------------------

func TestGitDataClientHTTPErrorPaths(t *testing.T) {
	t.Run("get returns 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		var result struct{}
		err := c.get("some/path", &result)
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("err = %v, want 500", err)
		}
	})

	t.Run("get network failure", func(t *testing.T) {
		// Port 1 is privileged and unused — Do() will fail.
		c := &GitDataClient{BaseURL: "http://127.0.0.1:1", Token: "t"}
		var result struct{}
		if err := c.get("x", &result); err == nil {
			t.Fatal("expected network error")
		}
	})

	t.Run("post returns 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		var result struct{}
		err := c.post("x", map[string]string{}, &result)
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("err = %v, want 500", err)
		}
	})

	t.Run("post network failure", func(t *testing.T) {
		c := &GitDataClient{BaseURL: "http://127.0.0.1:1", Token: "t"}
		var result struct{}
		if err := c.post("x", map[string]string{}, &result); err == nil {
			t.Fatal("expected network error")
		}
	})

	t.Run("postNoResponse returns 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		err := c.postNoResponse("x", map[string]string{})
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("err = %v, want 500", err)
		}
	})

	t.Run("postNoResponse network failure", func(t *testing.T) {
		c := &GitDataClient{BaseURL: "http://127.0.0.1:1", Token: "t"}
		if err := c.postNoResponse("x", map[string]string{}); err == nil {
			t.Fatal("expected network error")
		}
	})

	t.Run("patch returns 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		err := c.patch("x", map[string]string{})
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("err = %v, want 500", err)
		}
	})

	t.Run("patch network failure", func(t *testing.T) {
		c := &GitDataClient{BaseURL: "http://127.0.0.1:1", Token: "t"}
		if err := c.patch("x", map[string]string{}); err == nil {
			t.Fatal("expected network error")
		}
	})

	t.Run("get decode error on invalid JSON body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not valid json"))
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		var result struct {
			SHA string `json:"sha"`
		}
		if err := c.get("x", &result); err == nil {
			t.Fatal("expected decode error")
		}
	})

	t.Run("post decode error on invalid JSON body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not valid json"))
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		var result struct {
			SHA string `json:"sha"`
		}
		if err := c.post("x", map[string]string{}, &result); err == nil {
			t.Fatal("expected decode error")
		}
	})

	t.Run("httpDelete returns 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		err := c.httpDelete("x")
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("err = %v, want 500", err)
		}
	})

	t.Run("httpDelete network failure", func(t *testing.T) {
		c := &GitDataClient{BaseURL: "http://127.0.0.1:1", Token: "t"}
		if err := c.httpDelete("x"); err == nil {
			t.Fatal("expected network error")
		}
	})

	t.Run("httpDelete 404 returns ErrNotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		err := c.httpDelete("x")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("httpDelete 422 Reference does not exist returns ErrNotFound", func(t *testing.T) {
		// GitHub's Git Data API returns 422 (not 404) when deleting a
		// ref that was never created. We detect the specific message
		// so the CLI can show a clean "not found" error instead of
		// dumping the raw 422 JSON body.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Reference does not exist","documentation_url":"https://docs.github.com/rest/git/refs#delete-a-reference","status":"422"}`))
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		err := c.httpDelete("x")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("httpDelete 422 with different message is NOT ErrNotFound", func(t *testing.T) {
		// A genuine validation error (not "does not exist") should
		// fall through to the generic wrapped error, not ErrNotFound.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[]}`))
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		err := c.httpDelete("x")
		if err == nil {
			t.Fatal("expected error")
		}
		if errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, should NOT be ErrNotFound", err)
		}
	})

	t.Run("get 404 short-circuit", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		var result struct{}
		err := c.get("x", &result)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("err = %v, want 'not found'", err)
		}
	})
}

// ---------------------------------------------------------------------
// PushAttachments error paths at each API step.
// ---------------------------------------------------------------------

func TestPushAttachmentsErrorPaths(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "f.png")
	if err := os.WriteFile(testFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	repo := &Repo{Owner: "owner", Name: "repo"}

	t.Run("get commit on existing ref fails", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/1", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": map[string]string{"sha": "existing-sha"},
			})
		})
		mux.HandleFunc("GET /repos/owner/repo/git/commits/existing-sha", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "gone", http.StatusInternalServerError)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		_, _, err := c.PushAttachments(repo, "uploads/issues/1", "msg", []string{testFile})
		if err == nil || !strings.Contains(err.Error(), "get commit") {
			t.Errorf("err = %v, want 'get commit'", err)
		}
	})

	t.Run("create blob fails", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/2", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusForbidden)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		_, _, err := c.PushAttachments(repo, "uploads/issues/2", "msg", []string{testFile})
		if err == nil || !strings.Contains(err.Error(), "create blob") {
			t.Errorf("err = %v, want 'create blob'", err)
		}
	})

	t.Run("create tree fails", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/3", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob-sha"})
		})
		mux.HandleFunc("POST /repos/owner/repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad tree", http.StatusBadRequest)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		_, _, err := c.PushAttachments(repo, "uploads/issues/3", "msg", []string{testFile})
		if err == nil || !strings.Contains(err.Error(), "create tree") {
			t.Errorf("err = %v, want 'create tree'", err)
		}
	})

	t.Run("create commit fails", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/4", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "b"})
		})
		mux.HandleFunc("POST /repos/owner/repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "t"})
		})
		mux.HandleFunc("POST /repos/owner/repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad commit", http.StatusBadRequest)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		_, _, err := c.PushAttachments(repo, "uploads/issues/4", "msg", []string{testFile})
		if err == nil || !strings.Contains(err.Error(), "create commit") {
			t.Errorf("err = %v, want 'create commit'", err)
		}
	})

	t.Run("create ref fails", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/5", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "b"})
		})
		mux.HandleFunc("POST /repos/owner/repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "t"})
		})
		mux.HandleFunc("POST /repos/owner/repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "c"})
		})
		mux.HandleFunc("POST /repos/owner/repo/git/refs", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "ruleset", http.StatusUnprocessableEntity)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		_, _, err := c.PushAttachments(repo, "uploads/issues/5", "msg", []string{testFile})
		if err == nil || !strings.Contains(err.Error(), "create ref") {
			t.Errorf("err = %v, want 'create ref'", err)
		}
	})

	t.Run("update ref fails when ref already exists", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/6", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": map[string]string{"sha": "existing-sha"},
			})
		})
		mux.HandleFunc("GET /repos/owner/repo/git/commits/existing-sha", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"tree": map[string]string{"sha": "existing-tree"},
			})
		})
		mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "b"})
		})
		mux.HandleFunc("POST /repos/owner/repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "t"})
		})
		mux.HandleFunc("POST /repos/owner/repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "c"})
		})
		mux.HandleFunc("PATCH /repos/owner/repo/git/refs/uploads/issues/6", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not ff", http.StatusUnprocessableEntity)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		_, _, err := c.PushAttachments(repo, "uploads/issues/6", "msg", []string{testFile})
		if err == nil || !strings.Contains(err.Error(), "update ref") {
			t.Errorf("err = %v, want 'update ref'", err)
		}
	})

	t.Run("read file fails", func(t *testing.T) {
		c := &GitDataClient{BaseURL: "http://127.0.0.1:1", Token: "t"}
		// Non-existent file — os.ReadFile errors with "no such file".
		_, _, err := c.PushAttachments(repo, "uploads/issues/7", "msg", []string{"/tmp/does-not-exist-gh-attach-test.png"})
		if err == nil || !strings.Contains(err.Error(), "read") {
			t.Errorf("err = %v, want 'read' error", err)
		}
	})
}

// ---------------------------------------------------------------------
// parseRefEntry — pure function, unit tested without a server.
// ---------------------------------------------------------------------

func TestParseRefEntry(t *testing.T) {
	tests := []struct {
		name      string
		ref       string
		sha       string
		wantNS    string
		wantTgt   string
		wantNum   int
		wantKey   string
	}{
		{
			name:    "issue ref",
			ref:     "refs/uploads/issues/42",
			sha:     "abc",
			wantNS:  "issue",
			wantTgt: "#42",
			wantNum: 42,
		},
		{
			name:    "misc ref",
			ref:     "refs/uploads/misc/design-v2",
			sha:     "def",
			wantNS:  "misc",
			wantTgt: "misc/design-v2",
			wantKey: "design-v2",
		},
		{
			name:    "misc ref with subpath",
			ref:     "refs/uploads/misc/docs/arch",
			sha:     "ghi",
			wantNS:  "misc",
			wantTgt: "misc/docs/arch",
			wantKey: "docs/arch",
		},
		{
			name:    "issues ref with non-numeric target (unexpected)",
			ref:     "refs/uploads/issues/not-a-number",
			sha:     "jkl",
			wantNS:  "other",
			wantTgt: "issues/not-a-number",
		},
		{
			name:    "unknown namespace",
			ref:     "refs/uploads/other/whatever",
			sha:     "mno",
			wantNS:  "other",
			wantTgt: "other/whatever",
		},
		{
			name:    "no namespace segment",
			ref:     "refs/uploads/flat",
			sha:     "pqr",
			wantNS:  "other",
			wantTgt: "flat",
		},
		{
			name:    "not an uploads ref at all",
			ref:     "refs/heads/main",
			sha:     "stu",
			wantNS:  "other",
			wantTgt: "refs/heads/main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRefEntry(tt.ref, tt.sha)
			if got.Ref != tt.ref {
				t.Errorf("Ref = %q, want %q", got.Ref, tt.ref)
			}
			if got.SHA != tt.sha {
				t.Errorf("SHA = %q, want %q", got.SHA, tt.sha)
			}
			if got.Namespace != tt.wantNS {
				t.Errorf("Namespace = %q, want %q", got.Namespace, tt.wantNS)
			}
			if got.Target != tt.wantTgt {
				t.Errorf("Target = %q, want %q", got.Target, tt.wantTgt)
			}
			if got.Number != tt.wantNum {
				t.Errorf("Number = %d, want %d", got.Number, tt.wantNum)
			}
			if got.Key != tt.wantKey {
				t.Errorf("Key = %q, want %q", got.Key, tt.wantKey)
			}
		})
	}
}

// ---------------------------------------------------------------------
// ListRefs — mock-server tests for happy path, filter, and errors.
// ---------------------------------------------------------------------

func TestListRefs(t *testing.T) {
	repo := &Repo{Owner: "owner", Name: "repo"}

	canned := []map[string]interface{}{
		{
			"ref":    "refs/uploads/issues/42",
			"object": map[string]string{"sha": "sha42"},
		},
		{
			"ref":    "refs/uploads/misc/design-v2",
			"object": map[string]string{"sha": "sha-design"},
		},
	}

	t.Run("all refs (no subPrefix)", func(t *testing.T) {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(canned)
		}))
		defer srv.Close()

		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		entries, err := c.ListRefs(repo, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotPath != "/repos/owner/repo/git/matching-refs/uploads" {
			t.Errorf("API path = %q, want /repos/owner/repo/git/matching-refs/uploads", gotPath)
		}
		if len(entries) != 2 {
			t.Fatalf("got %d entries, want 2", len(entries))
		}
		if entries[0].Namespace != "issue" || entries[0].Number != 42 {
			t.Errorf("entry[0] = %+v", entries[0])
		}
		if entries[1].Namespace != "misc" || entries[1].Key != "design-v2" {
			t.Errorf("entry[1] = %+v", entries[1])
		}
	})

	t.Run("issues subPrefix", func(t *testing.T) {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(canned[:1])
		}))
		defer srv.Close()

		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		entries, err := c.ListRefs(repo, "issues")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotPath != "/repos/owner/repo/git/matching-refs/uploads/issues" {
			t.Errorf("API path = %q", gotPath)
		}
		if len(entries) != 1 || entries[0].Namespace != "issue" {
			t.Errorf("entries = %+v", entries)
		}
	})

	t.Run("misc subPrefix", func(t *testing.T) {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(canned[1:])
		}))
		defer srv.Close()

		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		entries, err := c.ListRefs(repo, "misc")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotPath != "/repos/owner/repo/git/matching-refs/uploads/misc" {
			t.Errorf("API path = %q", gotPath)
		}
		if len(entries) != 1 || entries[0].Namespace != "misc" {
			t.Errorf("entries = %+v", entries)
		}
	})

	t.Run("empty result", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]interface{}{})
		}))
		defer srv.Close()

		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		entries, err := c.ListRefs(repo, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("entries = %+v, want empty", entries)
		}
	})

	t.Run("server 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		defer srv.Close()

		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		_, err := c.ListRefs(repo, "")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// ---------------------------------------------------------------------
// DeleteRef — success, ErrNotFound, and transport errors.
// ---------------------------------------------------------------------

func TestDeleteRef(t *testing.T) {
	repo := &Repo{Owner: "owner", Name: "repo"}

	t.Run("success (204)", func(t *testing.T) {
		var gotMethod, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		if err := c.DeleteRef(repo, "uploads/misc/design-v2"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotMethod != "DELETE" {
			t.Errorf("method = %q, want DELETE", gotMethod)
		}
		if gotPath != "/repos/owner/repo/git/refs/uploads/misc/design-v2" {
			t.Errorf("path = %q", gotPath)
		}
	})

	t.Run("404 returns ErrNotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		err := c.DeleteRef(repo, "uploads/misc/nope")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("server 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		defer srv.Close()

		c := &GitDataClient{BaseURL: srv.URL, Token: "t"}
		err := c.DeleteRef(repo, "uploads/issues/42")
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want non-ErrNotFound", err)
		}
	})

	t.Run("network failure", func(t *testing.T) {
		c := &GitDataClient{BaseURL: "http://127.0.0.1:1", Token: "t"}
		if err := c.DeleteRef(repo, "uploads/issues/42"); err == nil {
			t.Fatal("expected network error")
		}
	})
}

// ---------------------------------------------------------------------
// GetAttachments — walks ref → commit → tree → blobs and returns raw
// bytes. Tests cover the happy paths (single + multi file), the
// not-found fast-path, and the decode/encoding failure modes so future
// edits can't silently break the exact inverse of PushAttachments.
// ---------------------------------------------------------------------

// getAttachmentsMux builds an httptest handler that serves a ref →
// commit → tree → blobs chain for `refPath` given a set of (basename,
// content) pairs. Each basename gets a synthetic blob SHA derived
// from its name so test assertions can reference them directly.
func getAttachmentsMux(t *testing.T, refPath string, files map[string]string) (http.Handler, *[]string) {
	t.Helper()
	mux := http.NewServeMux()
	var calls []string

	mux.HandleFunc(fmt.Sprintf("GET /repos/owner/repo/git/ref/%s", refPath), func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "GET ref")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": map[string]string{"sha": "tip-commit-sha"},
		})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/commits/tip-commit-sha", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "GET commit")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tree": map[string]string{"sha": "tree-sha-1"},
		})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/trees/tree-sha-1", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "GET tree")
		type entry struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		}
		// Stable iteration order for assertions: sort by path.
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		sort.Strings(names)
		entries := make([]entry, 0, len(names))
		for _, n := range names {
			entries = append(entries, entry{
				Path: n,
				Mode: "100644",
				Type: "blob",
				SHA:  "blob-" + n,
				Size: int64(len(files[n])),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tree": entries})
	})
	for name, content := range files {
		name, content := name, content
		mux.HandleFunc(fmt.Sprintf("GET /repos/owner/repo/git/blobs/blob-%s", name), func(w http.ResponseWriter, _ *http.Request) {
			calls = append(calls, "GET blob "+name)
			// Use line-wrapped base64 to match GitHub's real response
			// shape (60-column lines) — stripWhitespace must handle it.
			enc := base64.StdEncoding.EncodeToString([]byte(content))
			wrapped := ""
			for i := 0; i < len(enc); i += 60 {
				end := i + 60
				if end > len(enc) {
					end = len(enc)
				}
				wrapped += enc[i:end] + "\n"
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"sha":      "blob-" + name,
				"size":     len(content),
				"content":  wrapped,
				"encoding": "base64",
			})
		})
	}
	return mux, &calls
}

func TestGetAttachments_single_file(t *testing.T) {
	mux, calls := getAttachmentsMux(t, "uploads/issues/42", map[string]string{
		"shot.png": "PNG-BYTES-FOR-SHOT",
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	repo := &Repo{Owner: "owner", Name: "repo"}
	attachments, commitSHA, err := client.GetAttachments(repo, "uploads/issues/42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commitSHA != "tip-commit-sha" {
		t.Errorf("commitSHA = %q, want tip-commit-sha", commitSHA)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %v, want 1 entry", attachments)
	}
	got := attachments[0]
	if got.Path != "shot.png" {
		t.Errorf("path = %q, want shot.png", got.Path)
	}
	if string(got.Content) != "PNG-BYTES-FOR-SHOT" {
		t.Errorf("content = %q, want %q", got.Content, "PNG-BYTES-FOR-SHOT")
	}
	if got.SHA != "blob-shot.png" {
		t.Errorf("blob SHA = %q, want blob-shot.png", got.SHA)
	}
	if got.Size != int64(len("PNG-BYTES-FOR-SHOT")) {
		t.Errorf("size = %d, want %d", got.Size, len("PNG-BYTES-FOR-SHOT"))
	}

	expectedCalls := []string{"GET ref", "GET commit", "GET tree", "GET blob shot.png"}
	if len(*calls) != len(expectedCalls) {
		t.Fatalf("calls = %v, want %v", *calls, expectedCalls)
	}
	for i, c := range *calls {
		if c != expectedCalls[i] {
			t.Errorf("call[%d] = %q, want %q", i, c, expectedCalls[i])
		}
	}
}

func TestGetAttachments_multiple_files(t *testing.T) {
	mux, _ := getAttachmentsMux(t, "uploads/misc/design-v2", map[string]string{
		"before.png": "BEFORE-BYTES",
		"after.png":  "AFTER-BYTES",
		"diagram.md": "# Diagram\n\ntext here",
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	attachments, _, err := client.GetAttachments(&Repo{Owner: "owner", Name: "repo"}, "uploads/misc/design-v2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 3 {
		t.Fatalf("len = %d, want 3", len(attachments))
	}
	// getAttachmentsMux sorts tree entries by path, so expected order is
	// after.png, before.png, diagram.md.
	got := map[string]string{}
	for _, a := range attachments {
		got[a.Path] = string(a.Content)
	}
	want := map[string]string{
		"before.png": "BEFORE-BYTES",
		"after.png":  "AFTER-BYTES",
		"diagram.md": "# Diagram\n\ntext here",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetAttachments_ref_not_found(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/999", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	attachments, sha, err := client.GetAttachments(&Repo{Owner: "owner", Name: "repo"}, "uploads/issues/999")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want errors.Is(err, ErrNotFound) = true", err)
	}
	if attachments != nil {
		t.Errorf("attachments = %v, want nil", attachments)
	}
	if sha != "" {
		t.Errorf("sha = %q, want empty", sha)
	}
}

func TestGetAttachments_skips_non_blob_entries(t *testing.T) {
	// A tree that contains a subdirectory entry alongside a real blob.
	// GetAttachments must silently skip the non-blob so we don't
	// attempt to fetch a subtree as a blob and 404.
	mux := http.NewServeMux()
	var blobFetched bool

	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": map[string]string{"sha": "tip-commit-sha"},
		})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/commits/tip-commit-sha", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tree": map[string]string{"sha": "tree-sha-1"},
		})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/trees/tree-sha-1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tree": []map[string]interface{}{
				{"path": "subdir", "mode": "040000", "type": "tree", "sha": "subtree-sha"},
				{"path": "shot.png", "mode": "100644", "type": "blob", "sha": "blob-sha", "size": 3},
			},
		})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/blobs/blob-sha", func(w http.ResponseWriter, _ *http.Request) {
		blobFetched = true
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sha":      "blob-sha",
			"size":     3,
			"content":  base64.StdEncoding.EncodeToString([]byte("abc")),
			"encoding": "base64",
		})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/blobs/subtree-sha", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("blob fetch should not be attempted for subtree entries")
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	attachments, _, err := client.GetAttachments(&Repo{Owner: "owner", Name: "repo"}, "uploads/issues/1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !blobFetched {
		t.Error("expected blob-sha to be fetched")
	}
	if len(attachments) != 1 {
		t.Fatalf("len = %d, want 1 (subtree should be skipped)", len(attachments))
	}
	if attachments[0].Path != "shot.png" {
		t.Errorf("path = %q, want shot.png", attachments[0].Path)
	}
}

func TestGetAttachments_rejects_non_base64_encoding(t *testing.T) {
	// A blob response with encoding: "utf-8" (GitHub does this for
	// small text blobs if you don't set the Accept header — shouldn't
	// happen for us but we should fail loud if it ever does).
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": map[string]string{"sha": "c"}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/commits/c", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tree": map[string]string{"sha": "t"}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/trees/t", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tree": []map[string]interface{}{
			{"path": "f", "mode": "100644", "type": "blob", "sha": "b"},
		}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/blobs/b", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"content":  "hello",
			"encoding": "utf-8",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	_, _, err := client.GetAttachments(&Repo{Owner: "owner", Name: "repo"}, "uploads/issues/1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), `unexpected encoding "utf-8"`) {
		t.Errorf("err = %v, want 'unexpected encoding' substring", err)
	}
}

func TestGetAttachments_rejects_invalid_base64(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": map[string]string{"sha": "c"}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/commits/c", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tree": map[string]string{"sha": "t"}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/trees/t", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tree": []map[string]interface{}{
			{"path": "f", "mode": "100644", "type": "blob", "sha": "b"},
		}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/blobs/b", func(w http.ResponseWriter, _ *http.Request) {
		// Invalid base64 — `!@#$` are not in the base64 alphabet.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"content":  "!@#$",
			"encoding": "base64",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	_, _, err := client.GetAttachments(&Repo{Owner: "owner", Name: "repo"}, "uploads/issues/1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "decode blob") {
		t.Errorf("err = %v, want 'decode blob' substring", err)
	}
}

func TestGetAttachments_commit_fetch_error(t *testing.T) {
	// Ref resolves fine, but the commit fetch 500s. Confirms the error
	// is wrapped with "get commit:" prefix so the CLI layer can
	// surface it usefully.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": map[string]string{"sha": "c"}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/commits/c", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	_, _, err := client.GetAttachments(&Repo{Owner: "owner", Name: "repo"}, "uploads/issues/1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "get commit:") {
		t.Errorf("err = %v, want 'get commit:' prefix", err)
	}
}

func TestGetAttachments_ref_non_404_error(t *testing.T) {
	// Step-1 ref GET returns 500 (not 404). Confirms the non-404
	// branch wraps with "get ref:" instead of returning ErrNotFound.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"internal error"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	_, _, err := client.GetAttachments(&Repo{Owner: "owner", Name: "repo"}, "uploads/issues/1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, should NOT match ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "get ref:") {
		t.Errorf("err = %v, want 'get ref:' prefix", err)
	}
}

func TestGetAttachments_tree_fetch_error(t *testing.T) {
	// Steps 1 + 2 succeed, the tree GET 500s. Confirms the error
	// wraps with "get tree:" so callers can tell which step broke.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": map[string]string{"sha": "c"}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/commits/c", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tree": map[string]string{"sha": "t"}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/trees/t", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	_, _, err := client.GetAttachments(&Repo{Owner: "owner", Name: "repo"}, "uploads/issues/1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "get tree:") {
		t.Errorf("err = %v, want 'get tree:' prefix", err)
	}
}

func TestGetAttachments_blob_fetch_error(t *testing.T) {
	// Steps 1-3 succeed with one blob entry; the blob GET 500s.
	// Confirms the per-blob error is wrapped with "get blob ..." and
	// includes the blob SHA + path for debugging.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": map[string]string{"sha": "c"}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/commits/c", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tree": map[string]string{"sha": "t"}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/trees/t", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tree": []map[string]interface{}{
			{"path": "shot.png", "mode": "100644", "type": "blob", "sha": "b"},
		}})
	})
	mux.HandleFunc("GET /repos/owner/repo/git/blobs/b", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &GitDataClient{BaseURL: srv.URL, Token: "t"}
	_, _, err := client.GetAttachments(&Repo{Owner: "owner", Name: "repo"}, "uploads/issues/1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "get blob b (shot.png):") {
		t.Errorf("err = %v, want 'get blob b (shot.png):' prefix", err)
	}
}

// TestStripWhitespace exercises the small whitespace-stripping helper
// that handles GitHub's 60-column-wrapped base64 payloads.
func TestStripWhitespace(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"abc", "abc"},
		{"a b\tc", "abc"},
		{"line1\nline2\r\nline3", "line1line2line3"},
		{"  \t\n", ""},
	}
	for _, tt := range tests {
		if got := stripWhitespace(tt.in); got != tt.want {
			t.Errorf("stripWhitespace(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

