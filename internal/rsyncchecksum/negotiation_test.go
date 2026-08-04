package rsyncchecksum_test

import (
	"testing"

	"github.com/edsilegxrepo/rsync/internal/rsyncchecksum"
)

func TestNegotiateChecksumAlgorithm(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		err      bool
	}{
		{"xxhash sha1 md4", "xxhash", false},
		{"sha1 md4", "sha1", false},
		{"md4", "md4", false},
		{"md5", "md5", false},
		{"", "md4", false},
		{"unknown_algo", "", true},
	}

	for _, tt := range tests {
		got, err := rsyncchecksum.NegotiateChecksumAlgorithm(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("NegotiateChecksumAlgorithm(%q) err = %v, wantErr %v", tt.input, err, tt.err)
		}
		if got != tt.expected {
			t.Errorf("NegotiateChecksumAlgorithm(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
