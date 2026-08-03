package rsyncd

import (
	"testing"
)

func TestSanitizePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"../foo", "../foo"},
		{"../../../etc/passwd", "../../../etc/passwd"},
		{"/../", "."},
		{"/a/b/../c", "a/c"},
		{"sub/dir", "sub/dir"},
		{"", "."},
	}

	for _, tt := range tests {
		got := sanitizePath(tt.input)
		if got != tt.want {
			t.Errorf("sanitizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
