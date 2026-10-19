package chrome

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// cftIndex lists the latest Chrome for Testing builds with their download URLs.
const cftIndex = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

// browserGetter downloads a browser on demand, returning its path.
// *downloadedBrowser satisfies it.
type browserGetter interface {
	Get() (string, error)
}

// browserPath returns the path to a usable browser: an already-installed one,
// a previously downloaded one, or a freshly downloaded one (with consent).
func browserPath() (string, error) {
	if path, has := lookPath(); has {
		return path, nil
	}

	b, err := newDownloadedBrowser()
	if err != nil {
		return "", err
	}
	if b.valid() {
		return b.binPath(), nil
	}

	return downloadBrowser(b)
}

// downloadBrowser asks Consent and, only if granted, downloads a browser.
// A nil or declining Consent yields an error without downloading.
func downloadBrowser(b browserGetter) (string, error) {
	allow := false
	if Consent != nil {
		var err error
		if allow, err = Consent(); err != nil {
			return "", err
		}
	}
	if !allow {
		return "", fmt.Errorf("no Chrome/Chromium found and download not authorized (re-run with -allow-download to render the document)")
	}
	return b.Get()
}

// lookPath finds a Chrome, Chromium or Edge installed on the system.
func lookPath() (string, bool) {
	list := map[string][]string{
		"darwin": {
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
		},
		"linux": {
			"chrome",
			"google-chrome",
			"google-chrome-stable",
			"chromium",
			"chromium-browser",
			"microsoft-edge",
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/microsoft-edge",
			"/snap/bin/chromium",
			"/data/data/com.termux/files/usr/bin/chromium-browser",
		},
		"openbsd": {"chrome", "chromium"},
		"windows": append([]string{"chrome", "edge"}, windowsExePaths(
			`Google\Chrome\Application\chrome.exe`,
			`Chromium\Application\chrome.exe`,
			`Microsoft\Edge\Application\msedge.exe`,
		)...),
	}[runtime.GOOS]

	for _, p := range list {
		if found, err := exec.LookPath(p); err == nil {
			return found, true
		}
	}
	return "", false
}

func windowsExePaths(list ...string) []string {
	var out []string
	for _, p := range list {
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			out = append(out, filepath.Join(root, p))
		}
	}
	return out
}

// downloadedBrowser is a chrome-headless-shell build from Chrome for Testing,
// cached under dir.
type downloadedBrowser struct {
	dir      string
	platform string
	index    string
}

func newDownloadedBrowser() (*downloadedBrowser, error) {
	platform, err := cftPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("locate cache dir: %w", err)
	}
	return &downloadedBrowser{
		dir:      filepath.Join(cache, "md2", "chrome-headless-shell"),
		platform: platform,
		index:    cftIndex,
	}, nil
}

// cftPlatform maps a Go target to its Chrome for Testing platform name.
func cftPlatform(goos, goarch string) (string, error) {
	p, ok := map[string]string{
		"darwin/arm64":  "mac-arm64",
		"darwin/amd64":  "mac-x64",
		"linux/amd64":   "linux64",
		"linux/arm64":   "linux-arm64",
		"windows/amd64": "win64",
		"windows/386":   "win32",
	}[goos+"/"+goarch]
	if !ok {
		return "", fmt.Errorf("no Chromium download available for %s/%s; install Chrome or Chromium", goos, goarch)
	}
	return p, nil
}

func (b *downloadedBrowser) binPath() string {
	name := "chrome-headless-shell"
	if strings.HasPrefix(b.platform, "win") {
		name += ".exe"
	}
	return filepath.Join(b.dir, "chrome-headless-shell-"+b.platform, name)
}

func (b *downloadedBrowser) valid() bool {
	fi, err := os.Stat(b.binPath())
	return err == nil && fi.Mode().IsRegular()
}

// Get downloads and unpacks the browser into dir, returning its binary path.
// The archive is unpacked into a sibling directory first and moved into place
// only once complete, so an interrupted download never leaves a broken cache.
func (b *downloadedBrowser) Get() (string, error) {
	url, err := b.downloadURL()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(b.dir), 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(b.dir), "download-")
	if err != nil {
		return "", fmt.Errorf("create download dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	archive := filepath.Join(tmp, "browser.zip")
	if err := fetch(url, archive); err != nil {
		return "", err
	}
	unpacked := filepath.Join(tmp, "browser")
	if err := unzip(archive, unpacked); err != nil {
		return "", fmt.Errorf("unpack browser: %w", err)
	}

	if err := os.RemoveAll(b.dir); err != nil {
		return "", fmt.Errorf("clear old browser: %w", err)
	}
	if err := os.Rename(unpacked, b.dir); err != nil {
		return "", fmt.Errorf("install browser: %w", err)
	}
	if !b.valid() {
		return "", fmt.Errorf("downloaded browser has no %s", b.binPath())
	}
	return b.binPath(), nil
}

func (b *downloadedBrowser) downloadURL() (string, error) {
	resp, err := httpGet(b.index)
	if err != nil {
		return "", fmt.Errorf("fetch Chrome for Testing index: %w", err)
	}
	defer resp.Body.Close()

	var idx struct {
		Channels map[string]struct {
			Downloads map[string][]struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
			} `json:"downloads"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return "", fmt.Errorf("parse Chrome for Testing index: %w", err)
	}
	for _, d := range idx.Channels["Stable"].Downloads["chrome-headless-shell"] {
		if d.Platform == b.platform {
			return d.URL, nil
		}
	}
	return "", fmt.Errorf("no stable chrome-headless-shell build for %s", b.platform)
}

func httpGet(url string) (*http.Response, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

func fetch(url, dst string) error {
	resp, err := httpGet(url)
	if err != nil {
		return fmt.Errorf("download browser: %w", err)
	}
	defer resp.Body.Close()

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("download browser: %w", err)
	}
	return f.Close()
}

// unzip extracts archive into dst, keeping file modes (the browser binary must
// stay executable) and refusing entries that would land outside dst.
func unzip(archive, dst string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	root := filepath.Clean(dst) + string(os.PathSeparator)
	for _, f := range r.File {
		path := filepath.Join(dst, f.Name)
		if !strings.HasPrefix(path, root) {
			return fmt.Errorf("illegal path in archive: %s", f.Name)
		}
		if err := extract(f, path, root); err != nil {
			return err
		}
	}
	return nil
}

func extract(f *zip.File, path, root string) error {
	mode := f.Mode()
	if mode.IsDir() {
		return os.MkdirAll(path, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	if mode&os.ModeSymlink != 0 {
		target, err := io.ReadAll(rc)
		if err != nil {
			return err
		}
		if filepath.IsAbs(string(target)) || !strings.HasPrefix(filepath.Join(filepath.Dir(path), string(target)), root) {
			return fmt.Errorf("illegal symlink in archive: %s -> %s", f.Name, target)
		}
		return os.Symlink(string(target), path)
	}

	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
