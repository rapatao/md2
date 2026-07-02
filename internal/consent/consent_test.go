package consent

import (
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"YES\n", true},
		{" y \n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"maybe\n", false},
		{"", false}, // EOF, no input
	}
	for _, tt := range tests {
		got, err := Read(strings.NewReader(tt.in))
		if err != nil {
			t.Errorf("Read(%q): unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("Read(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestPolicy(t *testing.T) {
	// -allow-download authorizes unconditionally.
	if ok, err := Policy(true)(); err != nil || !ok {
		t.Errorf("Policy(true) = (%v, %v), want (true, nil)", ok, err)
	}
	// Without the flag and without an interactive terminal (the test
	// environment), it must decline rather than block on a prompt.
	if ok, err := Policy(false)(); err != nil || ok {
		t.Errorf("Policy(false) non-interactive = (%v, %v), want (false, nil)", ok, err)
	}
}
