package gh

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatComment(t *testing.T) {
	t.Run("single image with title", func(t *testing.T) {
		repo := &Repo{Owner: "enthus-appdev", Name: "negsoft-gui"}
		paths := []AttachmentPath{
			{Path: "screenshot.png"},
		}
		commitSHA := "1234567890abcdef1234567890abcdef12345678"
		body := formatComment(repo, paths, commitSHA, "After fix")

		if !strings.Contains(body, "<!-- gh-attach -->") {
			t.Error("missing marker")
		}
		if !strings.Contains(body, "**After fix**") {
			t.Error("missing title")
		}
		if !strings.Contains(body, "blob/1234567890abcdef1234567890abcdef12345678/screenshot.png?raw=true") {
			t.Error("missing image URL with commit SHA")
		}
	})

	t.Run("multiple images without title", func(t *testing.T) {
		repo := &Repo{Owner: "enthus-appdev", Name: "negsoft-gui"}
		paths := []AttachmentPath{
			{Path: "a.png"},
			{Path: "b.png"},
			{Path: "c.png"},
		}
		commitSHA := "1234567890abcdef1234567890abcdef12345678"
		body := formatComment(repo, paths, commitSHA, "")

		if strings.Contains(body, "****") {
			t.Error("empty title should not render")
		}
		if !strings.Contains(body, "| a.png | b.png |") {
			t.Error("expected 2-column grid header")
		}
	})

	t.Run("single image no title", func(t *testing.T) {
		repo := &Repo{Owner: "enthus-appdev", Name: "negsoft-gui"}
		paths := []AttachmentPath{
			{Path: "shot.png"},
		}
		commitSHA := "1234567890abcdef1234567890abcdef12345678"
		body := formatComment(repo, paths, commitSHA, "")

		if strings.Contains(body, "****") {
			t.Error("should not have empty title line")
		}
		if !strings.Contains(body, "![shot.png]") {
			t.Error("missing image markdown")
		}
	})
}

func TestUpsertCommentCreatesNew(t *testing.T) {
	var createdBody string

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]interface{}{})
	})

	mux.HandleFunc("POST /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		createdBody = body.Body
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":       1,
			"html_url": "https://github.com/owner/repo/pull/42#issuecomment-1",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &CommentClient{BaseURL: srv.URL, Token: "test-token"}
	repo := &Repo{Owner: "owner", Name: "repo"}
	paths := []AttachmentPath{{Path: "test.png"}}
	commitSHA := "deadbeefcafe1234567890abcdef1234567890ab"

	url, err := client.UpsertComment(repo, 42, paths, commitSHA, "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/owner/repo/pull/42#issuecomment-1" {
		t.Errorf("url = %q, want issuecomment-1 URL", url)
	}
	if !strings.Contains(createdBody, "<!-- gh-attach -->") {
		t.Error("created comment missing marker")
	}
	if !strings.Contains(createdBody, "blob/deadbeefcafe1234567890abcdef1234567890ab/test.png?raw=true") {
		t.Error("created comment missing commit-SHA-based URL")
	}
}

func TestUpsertCommentAppendsToExisting(t *testing.T) {
	var updatedBody string
	existingBody := "<!-- gh-attach -->\n### Attachments\n\n**Old upload**\n\n| old.png |\n|---|\n| ![old.png](url) |"

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]interface{}{
			map[string]interface{}{
				"id":       99,
				"body":     existingBody,
				"html_url": "https://github.com/owner/repo/pull/42#issuecomment-99",
			},
		})
	})

	mux.HandleFunc("PATCH /repos/owner/repo/issues/comments/99", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		updatedBody = body.Body
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":       99,
			"html_url": "https://github.com/owner/repo/pull/42#issuecomment-99",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &CommentClient{BaseURL: srv.URL, Token: "test-token"}
	repo := &Repo{Owner: "owner", Name: "repo"}
	paths := []AttachmentPath{{Path: "new.png"}}
	commitSHA := "feed1234cafebabe1234567890abcdef12345678"

	_, err := client.UpsertComment(repo, 42, paths, commitSHA, "New upload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(updatedBody, "Old upload") {
		t.Error("lost old content")
	}
	if !strings.Contains(updatedBody, "New upload") {
		t.Error("missing new content")
	}
	if !strings.Contains(updatedBody, "blob/feed1234cafebabe1234567890abcdef12345678/new.png?raw=true") {
		t.Error("appended section missing commit-SHA-based URL for new upload")
	}
}
