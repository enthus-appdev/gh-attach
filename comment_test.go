package main

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
		paths := []ScreenshotPath{
			{BranchPath: "pr-123/20260401-120000-screenshot.png", FileName: "screenshot.png"},
		}
		body := formatComment(repo, paths, "After fix")

		if !strings.Contains(body, "<!-- pr-screenshots -->") {
			t.Error("missing marker")
		}
		if !strings.Contains(body, "**After fix**") {
			t.Error("missing title")
		}
		if !strings.Contains(body, "blob/_screenshots/pr-123/20260401-120000-screenshot.png?raw=true") {
			t.Error("missing image URL")
		}
	})

	t.Run("multiple images without title", func(t *testing.T) {
		repo := &Repo{Owner: "enthus-appdev", Name: "negsoft-gui"}
		paths := []ScreenshotPath{
			{BranchPath: "pr-123/20260401-120000-a.png", FileName: "a.png"},
			{BranchPath: "pr-123/20260401-120000-b.png", FileName: "b.png"},
			{BranchPath: "pr-123/20260401-120000-c.png", FileName: "c.png"},
		}
		body := formatComment(repo, paths, "")

		if strings.Contains(body, "****") {
			t.Error("empty title should not render")
		}
		if !strings.Contains(body, "| a.png | b.png |") {
			t.Error("expected 2-column grid header")
		}
	})

	t.Run("single image no title", func(t *testing.T) {
		repo := &Repo{Owner: "enthus-appdev", Name: "negsoft-gui"}
		paths := []ScreenshotPath{
			{BranchPath: "pr-123/20260401-120000-shot.png", FileName: "shot.png"},
		}
		body := formatComment(repo, paths, "")

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
		json.NewEncoder(w).Encode([]interface{}{})
	})

	mux.HandleFunc("POST /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		createdBody = body.Body
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":       1,
			"html_url": "https://github.com/owner/repo/pull/42#issuecomment-1",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &CommentClient{BaseURL: srv.URL, Token: "test-token"}
	repo := &Repo{Owner: "owner", Name: "repo"}
	paths := []ScreenshotPath{{BranchPath: "pr-42/test.png", FileName: "test.png"}}

	url, err := client.UpsertComment(repo, 42, paths, "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/owner/repo/pull/42#issuecomment-1" {
		t.Errorf("url = %q, want issuecomment-1 URL", url)
	}
	if !strings.Contains(createdBody, "<!-- pr-screenshots -->") {
		t.Error("created comment missing marker")
	}
}

func TestUpsertCommentAppendsToExisting(t *testing.T) {
	var updatedBody string
	existingBody := "<!-- pr-screenshots -->\n### Screenshots\n\n**Old upload**\n\n| old.png |\n|---|\n| ![old.png](url) |"

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]interface{}{
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
		json.NewDecoder(r.Body).Decode(&body)
		updatedBody = body.Body
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":       99,
			"html_url": "https://github.com/owner/repo/pull/42#issuecomment-99",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &CommentClient{BaseURL: srv.URL, Token: "test-token"}
	repo := &Repo{Owner: "owner", Name: "repo"}
	paths := []ScreenshotPath{{BranchPath: "pr-42/new.png", FileName: "new.png"}}

	_, err := client.UpsertComment(repo, 42, paths, "New upload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(updatedBody, "Old upload") {
		t.Error("lost old content")
	}
	if !strings.Contains(updatedBody, "New upload") {
		t.Error("missing new content")
	}
}
