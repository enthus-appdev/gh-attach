package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPushScreenshotsCreatesBlob(t *testing.T) {
	var calls []string

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/42", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "GET ref")
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST blob")
		json.NewEncoder(w).Encode(map[string]string{"sha": "blob-sha-1"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST tree")
		json.NewEncoder(w).Encode(map[string]string{"sha": "tree-sha-1"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST commit")
		json.NewEncoder(w).Encode(map[string]string{"sha": "commit-sha-1"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/refs", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST ref")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"ref": "refs/uploads/issues/42"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.png")
	os.WriteFile(testFile, []byte("fake-png-data"), 0644)

	repo := &Repo{Owner: "owner", Name: "repo"}
	client := &GitDataClient{BaseURL: srv.URL, Token: "test-token"}

	paths, commitSHA, err := client.PushScreenshots(repo, 42, []string{testFile})
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

func TestPushScreenshotsAppendsToExistingBranch(t *testing.T) {
	var calls []string

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/owner/repo/git/ref/uploads/issues/42", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "GET ref")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": map[string]string{"sha": "existing-commit-sha"},
		})
	})

	mux.HandleFunc("GET /repos/owner/repo/git/commits/existing-commit-sha", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "GET commit")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tree": map[string]string{"sha": "existing-tree-sha"},
		})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST blob")
		json.NewEncoder(w).Encode(map[string]string{"sha": "blob-sha-1"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST tree")
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["base_tree"] != "existing-tree-sha" {
			t.Errorf("base_tree = %v, want %q", body["base_tree"], "existing-tree-sha")
		}
		json.NewEncoder(w).Encode(map[string]string{"sha": "new-tree-sha"})
	})

	mux.HandleFunc("POST /repos/owner/repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "POST commit")
		json.NewEncoder(w).Encode(map[string]string{"sha": "new-commit-sha"})
	})

	mux.HandleFunc("PATCH /repos/owner/repo/git/refs/uploads/issues/42", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "PATCH ref")
		json.NewEncoder(w).Encode(map[string]string{"ref": "refs/uploads/issues/42"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.png")
	os.WriteFile(testFile, []byte("fake-png-data"), 0644)

	repo := &Repo{Owner: "owner", Name: "repo"}
	client := &GitDataClient{BaseURL: srv.URL, Token: "test-token"}

	_, commitSHA, err := client.PushScreenshots(repo, 42, []string{testFile})
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
