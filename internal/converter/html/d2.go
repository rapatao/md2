package html

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	d2log "oss.terrastruct.com/d2/lib/log"
	"oss.terrastruct.com/d2/lib/textmeasure"
	"oss.terrastruct.com/util-go/go2"
)

// renderD2 compiles a d2 diagram's source into a standalone SVG element,
// rendered entirely in-process (no external binary, no browser). The SVG is
// emitted without an <?xml?> prolog so it can be inlined directly into an HTML
// body. Layout uses the pure-Go dagre engine, keeping the build CGO-free.
func renderD2(src []byte) ([]byte, error) {
	ruler, err := textmeasure.NewRuler()
	if err != nil {
		return nil, fmt.Errorf("d2 text ruler: %w", err)
	}

	layoutResolver := func(string) (d2graph.LayoutGraph, error) {
		return d2dagrelayout.DefaultLayout, nil
	}

	renderOpts := &d2svg.RenderOpts{
		Pad:      go2.Pointer(int64(5)),
		NoXMLTag: go2.Pointer(true),
	}

	// d2 logs internal debug/warn chatter through a context-carried slog.Logger
	// and dumps a stack trace when none is set; give it one that discards, so
	// md2's stderr stays clean.
	ctx := d2log.With(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	diagram, _, err := d2lib.Compile(ctx, string(src),
		&d2lib.CompileOptions{LayoutResolver: layoutResolver, Ruler: ruler}, renderOpts)
	if err != nil {
		return nil, fmt.Errorf("compile d2: %w", err)
	}

	svg, err := d2svg.Render(diagram, renderOpts)
	if err != nil {
		return nil, fmt.Errorf("render d2 svg: %w", err)
	}
	return svg, nil
}
