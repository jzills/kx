package main

// The site's icon set.
//
// Hugo serves a project's static/ ahead of the theme's, so every file written
// here shadows the Hextra icon of the same name — which is what the site
// showed before, a stock theme logo on a kx page. The names are Hextra's
// because its head partial is what links them.
//
// The mark is drawn without its shadow (mark.FaviconShapes) and coloured from
// the palette registry rather than a literal, so the icons a browser caches
// are the same green the site paints itself with. The site repaints the SVG
// from the reader's chosen palette at runtime; these files are what shows
// before that script runs, and in the raster sizes it can't reach.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/mark"
	"github.com/jzills/kx/internal/theme"
)

// rasters is every PNG the icon set needs, at the size its consumer asks for.
//
// The tab sizes are transparent so the mark sits on whatever the browser draws
// behind it. The home-screen sizes are not: iOS composites a transparent
// apple-touch-icon onto black, and Android draws the manifest icons on a
// launcher background, so both get the palette's own background painted in
// rather than borrowing one.
var rasters = []struct {
	path   string
	size   int
	opaque bool
}{
	{"site/static/favicon-16x16.png", 16, false},
	{"site/static/favicon-32x32.png", 32, false},
	{"site/static/apple-touch-icon.png", 180, true},
	{"site/static/android-chrome-192x192.png", 192, true},
	{"site/static/android-chrome-512x512.png", 512, true},
}

// icoSizes are the images packed into favicon.ico, which is the icon the
// browsers predating SVG favicons use, and the file anything crawling a site
// requests by convention whether or not a page links it.
var icoSizes = []int{16, 32}

func writeFavicons() error {
	styles, err := theme.WebStyles(theme.Default)
	if err != nil {
		return fmt.Errorf("palette %q: %w", theme.Default, err)
	}
	accent, err := parseHex(styles[theme.Accent])
	if err != nil {
		return fmt.Errorf("accent: %w", err)
	}
	background, err := parseHex(styles[theme.Background])
	if err != nil {
		return fmt.Errorf("background: %w", err)
	}

	// One file, read two ways: published verbatim as the /favicon.svg the head
	// links, and read by resources.Get so head-end.html can inline the same
	// shapes into the script that repaints them in the reader's palette. That
	// second reading is why site/hugo.toml mounts static/ into assets/ as
	// well — a copy under assets/ would be a copy that can go stale.
	icon := []byte(mark.Favicon(styles[theme.Accent]) + "\n")
	if err := write("site/static/favicon.svg", icon); err != nil {
		return err
	}

	for _, raster := range rasters {
		encoded, err := encodePNG(raster.size, accent, background, raster.opaque)
		if err != nil {
			return fmt.Errorf("%s: %w", raster.path, err)
		}
		if err := write(raster.path, encoded); err != nil {
			return err
		}
	}

	packed, err := encodeICO(accent, background)
	if err != nil {
		return fmt.Errorf("favicon.ico: %w", err)
	}
	if err := write("site/static/favicon.ico", packed); err != nil {
		return err
	}

	return write("site/static/site.webmanifest", []byte(manifest(styles)))
}

func write(path string, content []byte) error {
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	fmt.Println("updated", path)
	return nil
}

// draw rasterises the favicon tile at size pixels square.
//
// Block edges are snapped to whole pixels rather than antialiased, which is
// the raster equivalent of the shape-rendering="crispEdges" the SVGs carry:
// the mark is a grid of blocks, and a soft edge on a block is a smear, not a
// smoother curve. It matters most at the size it is hardest to read — a
// 288-unit tile at 16 pixels puts one pixel on each cell, and the half-cell
// margin would otherwise land the whole mark on half-pixels and blur every
// edge of it.
//
// Rounding both edges of a block the same way keeps neighbours touching, so
// the letterforms stay solid rather than growing seams.
func draw(size int, fg, bg color.RGBA, opaque bool) *image.RGBA {
	shapes, side := mark.FaviconShapes()
	scale := float64(size) / side

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	if opaque {
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = bg.R, bg.G, bg.B, 0xff
		}
	}

	for _, shape := range shapes {
		left := int(math.Round(shape.X * scale))
		top := int(math.Round(shape.Y * scale))
		right := int(math.Round((shape.X + shape.Width) * scale))
		bottom := int(math.Round((shape.Y + shape.Height) * scale))
		// A block that rounds away entirely would drop a cell out of the
		// letterform. No configured size is small enough for that — 16 pixels
		// is exactly one per cell — but a block is never nothing.
		if right <= left {
			right = left + 1
		}
		if bottom <= top {
			bottom = top + 1
		}

		for y := max(top, 0); y < min(bottom, size); y++ {
			for x := max(left, 0); x < min(right, size); x++ {
				img.SetRGBA(x, y, fg)
			}
		}
	}
	return img
}

func encodePNG(size int, fg, bg color.RGBA, opaque bool) ([]byte, error) {
	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&out, draw(size, fg, bg, opaque)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// encodeICO packs PNG images into an ICO container: a six-byte header, a
// sixteen-byte directory entry per image, then the images themselves. PNG
// payloads rather than the older bitmap encoding, which every browser that
// still reaches for a .ico has understood for a decade and a half.
func encodeICO(fg, bg color.RGBA) ([]byte, error) {
	images := make([][]byte, 0, len(icoSizes))
	for _, size := range icoSizes {
		// Transparent, like the other tab-sized icons: a .ico is drawn in the
		// same places favicon-16x16.png is.
		encoded, err := encodePNG(size, fg, bg, false)
		if err != nil {
			return nil, err
		}
		images = append(images, encoded)
	}

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(images))})

	offset := 6 + 16*len(images)
	for i, encoded := range images {
		out.Write([]byte{byte(icoSizes[i]), byte(icoSizes[i]), 0, 0})
		binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&out, binary.LittleEndian, uint32(len(encoded)))
		binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(encoded)
	}
	for _, encoded := range images {
		out.Write(encoded)
	}
	return out.Bytes(), nil
}

// manifest is the web app manifest, replacing Hextra's — which names the theme
// rather than the site, and paints an installed icon's surround black whatever
// the palette says.
func manifest(styles map[string]string) string {
	var out strings.Builder
	out.WriteString("{\n")
	out.WriteString("  \"name\": \"kx — kubectl, indexed\",\n")
	out.WriteString("  \"short_name\": \"kx\",\n")
	out.WriteString("  \"start_url\": \"index.html\",\n")
	out.WriteString("  \"icons\": [\n")
	for i, size := range []int{192, 512} {
		if i > 0 {
			out.WriteString(",\n")
		}
		fmt.Fprintf(&out, "    {\n      \"src\": \"android-chrome-%dx%d.png\",\n"+
			"      \"sizes\": \"%dx%d\",\n      \"type\": \"image/png\"\n    }",
			size, size, size, size)
	}
	out.WriteString("\n  ],\n")
	fmt.Fprintf(&out, "  \"theme_color\": %q,\n", styles[theme.Background])
	fmt.Fprintf(&out, "  \"background_color\": %q,\n", styles[theme.Background])
	out.WriteString("  \"display\": \"standalone\"\n")
	out.WriteString("}\n")
	return out.String()
}

// parseHex reads the #rrggbb the palettes are written in.
func parseHex(value string) (color.RGBA, error) {
	if len(value) != 7 || value[0] != '#' {
		return color.RGBA{}, fmt.Errorf("%q is not a #rrggbb colour", value)
	}
	channels := [3]uint8{}
	for i := range channels {
		component, err := strconv.ParseUint(value[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return color.RGBA{}, fmt.Errorf("%q: %w", value, err)
		}
		channels[i] = uint8(component)
	}
	return color.RGBA{channels[0], channels[1], channels[2], 0xff}, nil
}
