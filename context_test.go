package main

import (
	"strings"
	"testing"
)

func TestParseRepoFromRemote(t *testing.T) {
	tests := []struct {
		name    string
		remote  string
		owner   string
		repo    string
		wantErr bool
	}{
		{
			name:   "SSH URL",
			remote: "git@github.com:enthus-appdev/negsoft-gui.git",
			owner:  "enthus-appdev",
			repo:   "negsoft-gui",
		},
		{
			name:   "HTTPS URL",
			remote: "https://github.com/enthus-appdev/negsoft-gui.git",
			owner:  "enthus-appdev",
			repo:   "negsoft-gui",
		},
		{
			name:   "HTTPS URL without .git",
			remote: "https://github.com/enthus-appdev/negsoft-gui",
			owner:  "enthus-appdev",
			repo:   "negsoft-gui",
		},
		{
			name:    "invalid URL",
			remote:  "not-a-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := parseRepoFromRemote(tt.remote)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Owner != tt.owner {
				t.Errorf("owner = %q, want %q", r.Owner, tt.owner)
			}
			if r.Name != tt.repo {
				t.Errorf("name = %q, want %q", r.Name, tt.repo)
			}
		})
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantErr    bool
		errSubstr  string // when wantErr: expected substring in the error message
	}{
		// --- Accepted ---
		{name: "simple lowercase", key: "design-v2"},
		{name: "single char", key: "a"},
		{name: "leading underscore", key: "_internal"},
		{name: "mixed case", key: "DesignV2"},
		{name: "subpath", key: "docs/arch-diagram"},
		{name: "deep subpath", key: "releases/v1.0/screenshots"},
		{name: "starts with digit", key: "2026-report"},
		{name: "key with dot", key: "config.v1"},
		{name: "starts-with-numeric-but-not-all", key: "k123"},
		{name: "max length (100)", key: strings.Repeat("a", 100)},

		// --- Rejected: empty & length ---
		{name: "empty", key: "", wantErr: true, errSubstr: "empty"},
		{name: "too long (101)", key: strings.Repeat("a", 101), wantErr: true, errSubstr: "100 characters"},

		// --- Rejected: purely numeric (confusable with PR/issue number) ---
		{name: "pure numeric", key: "123", wantErr: true, errSubstr: "purely numeric"},
		{name: "single digit", key: "1", wantErr: true, errSubstr: "purely numeric"},

		// --- Rejected: leading character rules (whole-key level) ---
		{name: "leading dot (hidden)", key: ".hidden", wantErr: true, errSubstr: "start with '.'"},
		{name: "leading dash", key: "-foo", wantErr: true, errSubstr: "start with '-'"},
		{name: "leading slash", key: "/foo", wantErr: true, errSubstr: "start with '/'"},

		// --- Rejected: git ref name rules (whole-key level) ---
		{name: "contains ..", key: "foo..bar", wantErr: true, errSubstr: ".."},
		{name: "contains //", key: "foo//bar", wantErr: true, errSubstr: "//"},
		{name: "trailing slash", key: "foo/", wantErr: true, errSubstr: "end with '/'"},
		{name: "trailing dot", key: "design.", wantErr: true, errSubstr: "end with '.'"},
		{name: "ends with .lock (final segment)", key: "design.lock", wantErr: true, errSubstr: ".lock"},

		// --- Rejected: git ref name rules (per-segment) ---
		{name: "segment starts with dot", key: "foo/.bar", wantErr: true, errSubstr: "start with '.'"},
		{name: "segment starts with dash", key: "foo/-bar", wantErr: true, errSubstr: "start with '-'"},
		{name: "internal .lock segment", key: "foo.lock/bar", wantErr: true, errSubstr: ".lock"},
		{name: "hidden subpath", key: "docs/.git", wantErr: true, errSubstr: "start with '.'"},

		// --- Rejected: charset violations ---
		{name: "contains space", key: "foo bar", wantErr: true, errSubstr: "invalid"},
		{name: "contains @", key: "foo@bar", wantErr: true, errSubstr: "invalid"},
		{name: "contains :", key: "foo:bar", wantErr: true, errSubstr: "invalid"},
		{name: "contains ?", key: "foo?bar", wantErr: true, errSubstr: "invalid"},
		{name: "contains *", key: "foo*bar", wantErr: true, errSubstr: "invalid"},
		{name: "contains tilde", key: "foo~bar", wantErr: true, errSubstr: "invalid"},
		{name: "contains caret", key: "foo^bar", wantErr: true, errSubstr: "invalid"},
		{name: "contains backslash", key: "foo\\bar", wantErr: true, errSubstr: "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.key)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for %q: %v", tt.key, err)
			}
		})
	}
}
