package chrome

import (
	"errors"
	"testing"
)

// fakeBrowser records whether Get was called and returns a canned result.
type fakeBrowser struct {
	path   string
	err    error
	called bool
}

func (f *fakeBrowser) Get() (string, error) {
	f.called = true
	return f.path, f.err
}

func TestDownloadBrowserConsentGranted(t *testing.T) {
	prev := Consent
	defer func() { Consent = prev }()
	Consent = func() (bool, error) { return true, nil }

	fb := &fakeBrowser{path: "/tmp/chromium"}
	got, err := downloadBrowser(fb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/tmp/chromium" || !fb.called {
		t.Errorf("got (%q, called=%v), want (/tmp/chromium, called=true)", got, fb.called)
	}
}

func TestDownloadBrowserConsentDenied(t *testing.T) {
	prev := Consent
	defer func() { Consent = prev }()
	Consent = func() (bool, error) { return false, nil }

	fb := &fakeBrowser{path: "/tmp/chromium"}
	if _, err := downloadBrowser(fb); err == nil {
		t.Error("expected error when consent denied, got nil")
	}
	if fb.called {
		t.Error("Get must not be called when consent is denied")
	}
}

func TestDownloadBrowserNilConsent(t *testing.T) {
	prev := Consent
	defer func() { Consent = prev }()
	Consent = nil

	fb := &fakeBrowser{path: "/tmp/chromium"}
	if _, err := downloadBrowser(fb); err == nil {
		t.Error("expected error when Consent is nil, got nil")
	}
	if fb.called {
		t.Error("Get must not be called when Consent is nil")
	}
}

func TestDownloadBrowserConsentError(t *testing.T) {
	prev := Consent
	defer func() { Consent = prev }()
	sentinel := errors.New("prompt failed")
	Consent = func() (bool, error) { return false, sentinel }

	fb := &fakeBrowser{path: "/tmp/chromium"}
	_, err := downloadBrowser(fb)
	if !errors.Is(err, sentinel) {
		t.Errorf("want consent error propagated, got %v", err)
	}
	if fb.called {
		t.Error("Get must not be called when consent errors")
	}
}
