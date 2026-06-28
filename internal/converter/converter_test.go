package converter

import (
	"io"
	"sort"
	"testing"
)

// stubConverter is a no-op converter used to exercise the registry.
type stubConverter struct{}

func (stubConverter) Convert(_ []byte, _ io.Writer) error { return nil }

func TestRegisterAndGet(t *testing.T) {
	Register("stub", stubConverter{})

	got, err := Get("stub")
	if err != nil {
		t.Fatalf("Get(stub): unexpected error: %v", err)
	}
	if _, ok := got.(stubConverter); !ok {
		t.Fatalf("Get(stub) returned %T, want stubConverter", got)
	}
}

func TestGetUnknown(t *testing.T) {
	if _, err := Get("does-not-exist"); err == nil {
		t.Fatal("Get(does-not-exist): expected error, got nil")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	Register("dup", stubConverter{})

	defer func() {
		if recover() == nil {
			t.Fatal("Register duplicate: expected panic, got none")
		}
	}()
	Register("dup", stubConverter{})
}

func TestFormatsSorted(t *testing.T) {
	// Register out of order; Formats() must return a sorted list including them.
	Register("zfmt", stubConverter{})
	Register("afmt", stubConverter{})

	formats := Formats()
	if !sort.StringsAreSorted(formats) {
		t.Fatalf("Formats() not sorted: %v", formats)
	}
	for _, f := range []string{"afmt", "zfmt"} {
		if !contains(formats, f) {
			t.Errorf("Formats() missing %q: %v", f, formats)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
