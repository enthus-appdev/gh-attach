package main

import (
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
