package gh

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

