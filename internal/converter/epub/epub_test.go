package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readEPUB renders md and returns the archive reader plus a name->contents map.
func readEPUB(t *testing.T, md, baseDir string) (*zip.Reader, map[string]string) {
	t.Helper()
	data, err := Render([]byte(md), baseDir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	return zr, files
}

func TestMimetypeFirstAndStored(t *testing.T) {
	zr, _ := readEPUB(t, "# Hi\n\nbody\n", ".")
	first := zr.File[0]
	if first.Name != "mimetype" {
		t.Fatalf("first entry = %q, want mimetype", first.Name)
	}
	if first.Method != zip.Store {
		t.Errorf("mimetype must be stored uncompressed, got method %d", first.Method)
	}
	rc, _ := first.Open()
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "application/epub+zip" {
		t.Errorf("mimetype content = %q", b)
	}
}

func TestRequiredEntriesPresent(t *testing.T) {
	_, files := readEPUB(t, "# Hi\n\nbody\n", ".")
	for _, name := range []string{
		"META-INF/container.xml",
		"OEBPS/content.opf",
		"OEBPS/nav.xhtml",
		"OEBPS/content.xhtml",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("missing entry %s", name)
		}
	}
}

func TestChapterIsWellFormedXHTML(t *testing.T) {
	_, files := readEPUB(t, "# Title\n\nHello **world**.\n\n---\n", ".")
	chapter := files["OEBPS/content.xhtml"]
	// A token loop over the whole document fails on any malformed XML — this is
	// what proves WithXHTML self-closed the <hr/> void element.
	dec := xml.NewDecoder(strings.NewReader(chapter))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("chapter is not well-formed XML: %v", err)
		}
	}
	if !strings.Contains(chapter, "Hello") {
		t.Errorf("rendered text missing from chapter: %q", chapter)
	}
}

func TestTitleFromHeading(t *testing.T) {
	_, files := readEPUB(t, "# My Book\n\nbody\n", ".")
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:title>My Book</dc:title>") {
		t.Errorf("title not derived from H1: %q", files["OEBPS/content.opf"])
	}
}

func TestTitleFallsBackToUntitled(t *testing.T) {
	_, files := readEPUB(t, "just a paragraph, no heading\n", ".")
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:title>Untitled</dc:title>") {
		t.Errorf("missing Untitled fallback: %q", files["OEBPS/content.opf"])
	}
}

func TestLocalImagePackaged(t *testing.T) {
	dir := t.TempDir()
	// A tiny valid PNG isn't needed — packaging only reads and copies bytes.
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, files := readEPUB(t, "![alt](pic.png)\n", dir)

	if got := files["OEBPS/images/img1.png"]; got != "PNGDATA" {
		t.Errorf("image not packaged: %q", got)
	}
	if !strings.Contains(files["OEBPS/content.opf"], `media-type="image/png"`) {
		t.Errorf("image manifest entry missing: %q", files["OEBPS/content.opf"])
	}
	if !strings.Contains(files["OEBPS/content.xhtml"], `src="images/img1.png"`) {
		t.Errorf("image src not rewritten: %q", files["OEBPS/content.xhtml"])
	}
}

func TestMissingImageDoesNotFail(t *testing.T) {
	_, files := readEPUB(t, "![alt](nope.png)\n", t.TempDir())
	// Render succeeds and the original ref is left in place.
	if !strings.Contains(files["OEBPS/content.xhtml"], `src="nope.png"`) {
		t.Errorf("missing image ref should be left as-is: %q", files["OEBPS/content.xhtml"])
	}
	if strings.Contains(files["OEBPS/content.opf"], "images/") {
		t.Errorf("no image should be packaged: %q", files["OEBPS/content.opf"])
	}
}

func TestConverterConvert(t *testing.T) {
	var buf bytes.Buffer
	if err := (Converter{}).Convert([]byte("# Hi\n"), &buf); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if _, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Errorf("Convert output is not a valid zip: %v", err)
	}
}
