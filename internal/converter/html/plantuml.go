package html

import (
	"bytes"
	"compress/flate"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// PlantUMLServer is the base URL of the PlantUML server used to render
// `plantuml` diagrams to SVG at build time. PlantUML has no pure-Go or
// client-side renderer, so — unlike d2 (compiled in-process) — md2 must encode
// the diagram source and fetch the rendered SVG from a server, then inline it
// so the output stays self-contained. Defaults to the public plantuml.com
// server; set from the -plantuml-server CLI flag (e.g. a self-hosted server for
// offline or private use). Note that rendering sends the diagram source to this
// server.
var PlantUMLServer = "https://www.plantuml.com/plantuml"

// plantumlTimeout bounds a single diagram render request to the PlantUML server.
const plantumlTimeout = 30 * time.Second

// renderPlantUML encodes a plantuml diagram's source per PlantUML's text
// encoding, fetches the rendered SVG from PlantUMLServer, and returns it ready
// to inline into an HTML body (the leading <?xml?>/<!DOCTYPE> prolog is
// stripped, mirroring d2's NoXMLTag output). Any network or server error is
// returned so the caller can fall back to rendering the block as plain code.
func renderPlantUML(src []byte) ([]byte, error) {
	url := PlantUMLServer + "/svg/" + plantumlEncode(bytes.TrimRight(src, "\n"))

	client := &http.Client{Timeout: plantumlTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch plantuml svg: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read plantuml svg: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plantuml server returned %s", resp.Status)
	}
	return stripXMLProlog(body), nil
}

// svgPrologRe matches a leading <?xml?> declaration and/or <!DOCTYPE ...> so the
// SVG can be inlined directly into an HTML document.
var svgPrologRe = regexp.MustCompile(`(?is)^\s*(<\?xml[^>]*\?>\s*)?(<!doctype[^>]*>\s*)?`)

func stripXMLProlog(svg []byte) []byte {
	return svgPrologRe.ReplaceAll(svg, nil)
}

// plantumlEncode encodes diagram source using PlantUML's text-encoding scheme:
// raw-deflate the UTF-8 source, then map every 3 bytes to 4 characters of
// PlantUML's own base64 alphabet. The resulting string is what a PlantUML server
// expects in a /svg/<encoded> request.
func plantumlEncode(src []byte) string {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	_, _ = w.Write(src)
	_ = w.Close()
	return encode64(buf.Bytes())
}

// plantumlAlphabet is PlantUML's base64 variant, in the exact order its
// encode6bit uses: digits, uppercase, lowercase, then '-' and '_'.
const plantumlAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-_"

// encode64 maps bytes to PlantUML's base64 alphabet, 3 input bytes to 4 output
// characters (the final group is padded with zero bytes, as PlantUML does).
func encode64(data []byte) string {
	var b bytes.Buffer
	for i := 0; i < len(data); i += 3 {
		var b1, b2, b3 byte
		b1 = data[i]
		if i+1 < len(data) {
			b2 = data[i+1]
		}
		if i+2 < len(data) {
			b3 = data[i+2]
		}
		b.WriteByte(plantumlAlphabet[b1>>2])
		b.WriteByte(plantumlAlphabet[((b1&0x3)<<4)|(b2>>4)])
		b.WriteByte(plantumlAlphabet[((b2&0xF)<<2)|(b3>>6)])
		b.WriteByte(plantumlAlphabet[b3&0x3F])
	}
	return b.String()
}
