package cli

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantN    int
		wantRest []string
	}{
		{
			name:     "empty args",
			args:     []string{},
			wantN:    0,
			wantRest: nil,
		},
		{
			name:     "number plus single file",
			args:     []string{"42", "a.png"},
			wantN:    42,
			wantRest: []string{"a.png"},
		},
		{
			name:     "number plus multiple files",
			args:     []string{"123", "a.png", "b.png", "c.png"},
			wantN:    123,
			wantRest: []string{"a.png", "b.png", "c.png"},
		},
		{
			name:     "files only (no leading number)",
			args:     []string{"file.png"},
			wantN:    0,
			wantRest: []string{"file.png"},
		},
		{
			name:     "number only, no files",
			args:     []string{"42"},
			wantN:    42,
			wantRest: []string{},
		},
		{
			name:     "negative number treated as file",
			args:     []string{"-1", "file.png"},
			wantN:    0,
			wantRest: []string{"-1", "file.png"},
		},
		{
			name:     "zero treated as file",
			args:     []string{"0", "file.png"},
			wantN:    0,
			wantRest: []string{"0", "file.png"},
		},
		{
			name:     "alphanumeric first arg is not a number",
			args:     []string{"42a", "file.png"},
			wantN:    0,
			wantRest: []string{"42a", "file.png"},
		},
		{
			name:     "filename that looks like a path",
			args:     []string{"./images/shot.png"},
			wantN:    0,
			wantRest: []string{"./images/shot.png"},
		},
		{
			name:     "leading number preserves trailing files verbatim",
			args:     []string{"7", "/tmp/a", "/tmp/b"},
			wantN:    7,
			wantRest: []string{"/tmp/a", "/tmp/b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, rest := parseArgs(tt.args)
			if n != tt.wantN {
				t.Errorf("number = %d, want %d", n, tt.wantN)
			}
			if !reflect.DeepEqual(rest, tt.wantRest) {
				t.Errorf("rest = %#v, want %#v", rest, tt.wantRest)
			}
		})
	}
}
