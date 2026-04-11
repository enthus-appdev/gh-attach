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
		repo := &Repo{Owner: "enthus-appdev", Name: "demo-repo"}
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
		repo := &Repo{Owner: "enthus-appdev", Name: "demo-repo"}
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

	t.Run("filename with special characters is URL-encoded", func(t *testing.T) {
		// Filenames can contain spaces, hashes, and non-ASCII characters.
		// The rendered URL must URL-encode them so the link works in a
		// browser; the display name (alt text) must stay unencoded so
		// users see the original filename.
		repo := &Repo{Owner: "enthus-appdev", Name: "demo-repo"}
		paths := []AttachmentPath{
			{Path: "Screen Shot 2026.png"},
			{Path: "café#1.png"},
		}
		commitSHA := "abc1234"
		body := formatComment(repo, paths, commitSHA, "")

		// URL should be encoded
		if !strings.Contains(body, "Screen%20Shot%202026.png?raw=true") {
			t.Errorf("expected URL-encoded filename in body:\n%s", body)
		}
		if !strings.Contains(body, "caf%C3%A9%231.png?raw=true") {
			t.Errorf("expected URL-encoded UTF-8 + hash in body:\n%s", body)
		}
		// Display name (alt text + table header) should be raw
		if !strings.Contains(body, "![Screen Shot 2026.png]") {
			t.Errorf("expected raw display name in image alt:\n%s", body)
		}
		if !strings.Contains(body, "| Screen Shot 2026.png |") {
			t.Errorf("expected raw display name in table header:\n%s", body)
		}
	})

	t.Run("single image no title", func(t *testing.T) {
		repo := &Repo{Owner: "enthus-appdev", Name: "demo-repo"}
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

// ---------------------------------------------------------------------
// NewCommentClient — uses stubbed execCommand.
// ---------------------------------------------------------------------

func TestNewCommentClient(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		withStubExec(t, stubOutput("gho_fake"))
		client, err := NewCommentClient()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client.Token != "gho_fake" {
			t.Errorf("token = %q, want gho_fake", client.Token)
		}
		if client.BaseURL != "https://api.github.com" {
			t.Errorf("baseURL = %q, want https://api.github.com", client.BaseURL)
		}
	})

	t.Run("gh auth token fails", func(t *testing.T) {
		withStubExec(t, stubFail())
		_, err := NewCommentClient()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// ---------------------------------------------------------------------
// UpsertComment error paths.
// ---------------------------------------------------------------------

func TestUpsertCommentErrorPaths(t *testing.T) {
	repo := &Repo{Owner: "owner", Name: "repo"}
	paths := []AttachmentPath{{Path: "f.png"}}

	t.Run("list comments 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := &CommentClient{BaseURL: srv.URL, Token: "t"}
		_, err := c.UpsertComment(repo, 42, paths, "sha", "")
		if err == nil || !strings.Contains(err.Error(), "list comments") {
			t.Errorf("err = %v, want 'list comments'", err)
		}
	})

	t.Run("list comments network failure", func(t *testing.T) {
		c := &CommentClient{BaseURL: "http://127.0.0.1:1", Token: "t"}
		_, err := c.UpsertComment(repo, 42, paths, "sha", "")
		if err == nil {
			t.Fatal("expected network error")
		}
	})

	t.Run("create comment 500", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
			// Empty list → triggers create path.
			_ = json.NewEncoder(w).Encode([]interface{}{})
		})
		mux.HandleFunc("POST /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &CommentClient{BaseURL: srv.URL, Token: "t"}
		_, err := c.UpsertComment(repo, 42, paths, "sha", "")
		if err == nil || !strings.Contains(err.Error(), "create comment") {
			t.Errorf("err = %v, want 'create comment'", err)
		}
	})

	t.Run("update comment 500", func(t *testing.T) {
		mux := http.NewServeMux()
		existingBody := "<!-- gh-attach -->\n### Attachments\n\n**Old**\n"
		mux.HandleFunc("GET /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]interface{}{
				map[string]interface{}{
					"id":       99,
					"body":     existingBody,
					"html_url": "https://example",
				},
			})
		})
		mux.HandleFunc("PATCH /repos/owner/repo/issues/comments/99", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &CommentClient{BaseURL: srv.URL, Token: "t"}
		_, err := c.UpsertComment(repo, 42, paths, "sha", "")
		if err == nil || !strings.Contains(err.Error(), "update comment") {
			t.Errorf("err = %v, want 'update comment'", err)
		}
	})

	t.Run("create comment decode error", func(t *testing.T) {
		// Server returns 200 but the body is not JSON — the Decode() call
		// in createComment errors out and UpsertComment propagates it.
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]interface{}{})
		})
		mux.HandleFunc("POST /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not json"))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &CommentClient{BaseURL: srv.URL, Token: "t"}
		_, err := c.UpsertComment(repo, 42, paths, "sha", "")
		if err == nil {
			t.Fatal("expected decode error")
		}
	})

	t.Run("update comment decode error", func(t *testing.T) {
		mux := http.NewServeMux()
		existingBody := "<!-- gh-attach -->\n"
		mux.HandleFunc("GET /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]interface{}{
				map[string]interface{}{
					"id":       99,
					"body":     existingBody,
					"html_url": "https://example",
				},
			})
		})
		mux.HandleFunc("PATCH /repos/owner/repo/issues/comments/99", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not json"))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &CommentClient{BaseURL: srv.URL, Token: "t"}
		_, err := c.UpsertComment(repo, 42, paths, "sha", "")
		if err == nil {
			t.Fatal("expected decode error")
		}
	})

	t.Run("list comments decode error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		defer srv.Close()
		c := &CommentClient{BaseURL: srv.URL, Token: "t"}
		_, err := c.UpsertComment(repo, 42, paths, "sha", "")
		if err == nil {
			t.Fatal("expected decode error")
		}
	})

	t.Run("create comment network failure", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]interface{}{})
		})
		// POST handler absent — mux returns 404 for the create.
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &CommentClient{BaseURL: srv.URL, Token: "t"}
		_, err := c.UpsertComment(repo, 42, paths, "sha", "")
		if err == nil || !strings.Contains(err.Error(), "create comment") {
			t.Errorf("err = %v, want 'create comment'", err)
		}
	})

	t.Run("update returns no html_url so existingURL is used", func(t *testing.T) {
		// Covers the fallback: if updateComment returns an empty URL,
		// UpsertComment returns the existingURL from the listing instead.
		mux := http.NewServeMux()
		existingBody := "<!-- gh-attach -->\n### Attachments\n\n**Old**\n"
		mux.HandleFunc("GET /repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]interface{}{
				map[string]interface{}{
					"id":       99,
					"body":     existingBody,
					"html_url": "https://fallback-url",
				},
			})
		})
		mux.HandleFunc("PATCH /repos/owner/repo/issues/comments/99", func(w http.ResponseWriter, r *http.Request) {
			// Return 200 with no html_url field in the body → updateComment
			// returns "" for the URL, and UpsertComment falls back to existingURL.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": 99})
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := &CommentClient{BaseURL: srv.URL, Token: "t"}
		url, err := c.UpsertComment(repo, 42, paths, "sha", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://fallback-url" {
			t.Errorf("url = %q, want https://fallback-url (fallback)", url)
		}
	})
}

// ---------------------------------------------------------------------
// isImage + FormatSection non-image rendering
// ---------------------------------------------------------------------

func TestIsImage(t *testing.T) {
	t.Run("image extensions are recognized", func(t *testing.T) {
		good := []string{
			"shot.png", "photo.jpg", "photo.jpeg", "anim.gif",
			"small.webp", "icon.svg", "classic.bmp", "favicon.ico",
			"anim.apng", "modern.avif", "iphone.heic", "same.heif",
			// Case-insensitive match.
			"SCREAM.PNG", "IMG_1234.JPG", "Camera.JpeG",
			// Spaces and non-ASCII in the base are fine; only the
			// extension is inspected.
			"Screen Shot 2026.png", "café#1.jpeg",
		}
		for _, n := range good {
			if !isImage(n) {
				t.Errorf("isImage(%q) = false, want true", n)
			}
		}
	})
	t.Run("non-image extensions are rejected", func(t *testing.T) {
		bad := []string{
			"doc.pdf", "report.md", "notes.txt", "archive.zip",
			"log.log", "data.csv", "trace.har", "video.mp4",
			"bundle.tar.gz", "Makefile", // no extension at all
			"README",                  // no extension
			"script.sh", "config.yml", // text formats
			// Double extension: only the last one counts.
			"shot.png.bak",
			// Leading-dot file with no extension is not an image.
			".hidden",
		}
		for _, n := range bad {
			if isImage(n) {
				t.Errorf("isImage(%q) = true, want false", n)
			}
		}
	})
}

func TestFormatSection_non_image_files(t *testing.T) {
	// A pure non-image upload (PDFs, logs, archives) must render as
	// plain links, not broken image embeds.
	repo := &Repo{Owner: "owner", Name: "repo"}
	paths := []AttachmentPath{
		{Path: "report.pdf"},
		{Path: "trace.log"},
	}
	body := FormatSection(repo, paths, "abc1234", "")

	// No image-embed syntax anywhere.
	if strings.Contains(body, "![report.pdf") {
		t.Errorf("non-image rendered as image embed:\n%s", body)
	}
	if strings.Contains(body, "![trace.log") {
		t.Errorf("non-image rendered as image embed:\n%s", body)
	}

	// Both files rendered as plain markdown links.
	if !strings.Contains(body, "[report.pdf](https://github.com/owner/repo/blob/abc1234/report.pdf?raw=true)") {
		t.Errorf("missing plain link for report.pdf:\n%s", body)
	}
	if !strings.Contains(body, "[trace.log](https://github.com/owner/repo/blob/abc1234/trace.log?raw=true)") {
		t.Errorf("missing plain link for trace.log:\n%s", body)
	}
	// Single-column table header for each file.
	if !strings.Contains(body, "| report.pdf |") {
		t.Errorf("missing table header for report.pdf:\n%s", body)
	}
}

func TestFormatSection_mixed_image_and_non_image(t *testing.T) {
	// Mixed uploads must render images in the image table and
	// non-images in the separate plain-link table below it.
	repo := &Repo{Owner: "owner", Name: "repo"}
	paths := []AttachmentPath{
		{Path: "before.png"},
		{Path: "report.pdf"},
		{Path: "after.png"},
		{Path: "trace.log"},
	}
	body := FormatSection(repo, paths, "abc1234", "Mixed upload")

	// Title is rendered once, at the top.
	if !strings.Contains(body, "**Mixed upload**") {
		t.Errorf("missing title:\n%s", body)
	}

	// Images use the embed syntax.
	if !strings.Contains(body, "![before.png](") {
		t.Errorf("before.png should render as image embed:\n%s", body)
	}
	if !strings.Contains(body, "![after.png](") {
		t.Errorf("after.png should render as image embed:\n%s", body)
	}

	// Non-images use the link syntax (NOT the embed syntax).
	if strings.Contains(body, "![report.pdf") {
		t.Errorf("report.pdf should NOT render as image embed:\n%s", body)
	}
	if strings.Contains(body, "![trace.log") {
		t.Errorf("trace.log should NOT render as image embed:\n%s", body)
	}
	if !strings.Contains(body, "[report.pdf](https://github.com/owner/repo/blob/abc1234/report.pdf?raw=true)") {
		t.Errorf("missing plain link for report.pdf:\n%s", body)
	}
	if !strings.Contains(body, "[trace.log](https://github.com/owner/repo/blob/abc1234/trace.log?raw=true)") {
		t.Errorf("missing plain link for trace.log:\n%s", body)
	}

	// With two images, they go into a single 2-column row (existing
	// layout for multiple images). Assert the 2-column header appears.
	if !strings.Contains(body, "| before.png | after.png |") {
		t.Errorf("expected 2-column image header:\n%s", body)
	}

	// Images come BEFORE non-images in the output.
	imgIdx := strings.Index(body, "![before.png]")
	pdfIdx := strings.Index(body, "[report.pdf](")
	if imgIdx < 0 || pdfIdx < 0 {
		t.Fatalf("missing expected substrings, body:\n%s", body)
	}
	if imgIdx > pdfIdx {
		t.Errorf("images should render before non-images, got img=%d pdf=%d\n%s", imgIdx, pdfIdx, body)
	}
}

func TestFormatSection_case_insensitive_image_extensions(t *testing.T) {
	// Screenshots from iPhone / macOS often have uppercase extensions
	// like `IMG_1234.HEIC` or `Photo.JPG`. They must be treated as
	// images (render as embed, not link).
	repo := &Repo{Owner: "owner", Name: "repo"}
	paths := []AttachmentPath{
		{Path: "IMG_1234.HEIC"},
		{Path: "Photo.JPG"},
	}
	body := FormatSection(repo, paths, "sha", "")

	if !strings.Contains(body, "![IMG_1234.HEIC](") {
		t.Errorf("uppercase .HEIC should render as image:\n%s", body)
	}
	if !strings.Contains(body, "![Photo.JPG](") {
		t.Errorf("uppercase .JPG should render as image:\n%s", body)
	}
	// Neither should appear as a plain link.
	if strings.Contains(body, "[IMG_1234.HEIC](") && !strings.Contains(body, "![IMG_1234.HEIC](") {
		t.Errorf("IMG_1234.HEIC rendered as link, want image embed:\n%s", body)
	}
}

func TestFormatSection_single_non_image(t *testing.T) {
	// Single non-image upload produces one link row, no image table
	// at all. Confirms the "images section absent" path doesn't
	// leave stray table headers or empty rows.
	repo := &Repo{Owner: "owner", Name: "repo"}
	paths := []AttachmentPath{{Path: "release-notes.pdf"}}
	body := FormatSection(repo, paths, "sha", "")

	if strings.Contains(body, "![release-notes.pdf") {
		t.Errorf("single non-image rendered as image embed:\n%s", body)
	}
	if !strings.Contains(body, "[release-notes.pdf](https://github.com/owner/repo/blob/sha/release-notes.pdf?raw=true)") {
		t.Errorf("missing plain link:\n%s", body)
	}
	if !strings.Contains(body, "| release-notes.pdf |") {
		t.Errorf("missing table header:\n%s", body)
	}
}

