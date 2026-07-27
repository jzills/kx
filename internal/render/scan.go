package render

import (
	"strconv"

	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/theme"
)

// scanSeverity maps a CVE bucket onto its column header and the style a nonzero
// count is drawn in. Critical and high share the alarm colour; medium is a
// warning; low and unspecified stay muted so the eye lands on what's
// actionable. Order matches scanner.Severities.
var scanSeverity = map[string]struct{ Header, Style string }{
	"CRITICAL":    {"CRIT", theme.StatusBad},
	"HIGH":        {"HIGH", theme.StatusBad},
	"MEDIUM":      {"MED", theme.StatusWarn},
	"LOW":         {"LOW", theme.Muted},
	"UNSPECIFIED": {"UNSPEC", theme.Muted},
}

// ScanSummary renders one row per unique image: severity counts coloured by
// bucket, with a trailing status cell for images that failed to scan.
//
// Counts of zero stay muted so nonzero criticals and highs stand out.
func (r *Renderer) ScanSummary(rows []scanner.ImageScan) {
	// IMAGE absorbs the width pressure and ellipsizes; the count columns are
	// narrow and fixed.
	columns := []Column{{Header: "IMAGE", Flex: true}}
	for _, severity := range scanner.Severities {
		columns = append(columns, Column{Header: scanSeverity[severity].Header, Right: true})
	}
	columns = append(columns, Column{Header: ""})

	cells := make([][]Cell, 0, len(rows))
	for _, row := range rows {
		line := []Cell{Plain(row.Image)}
		if row.Counts == nil {
			// The scan failed; dashes rather than zeroes, which would read as
			// a clean result.
			for range scanner.Severities {
				line = append(line, Styled("—", theme.Muted))
			}
			message := row.Error
			if message == "" {
				message = "error"
			}
			cells = append(cells, append(line, Styled(message, theme.Error)))
			continue
		}
		for _, severity := range scanner.Severities {
			count := row.Counts[severity]
			style := theme.Muted
			if count > 0 {
				style = scanSeverity[severity].Style
			}
			line = append(line, Styled(strconv.Itoa(count), style))
		}
		cells = append(cells, append(line, Plain("")))
	}
	r.Table(columns, cells)
}

// ScanSummary renders through the package-level renderer.
func ScanSummary(rows []scanner.ImageScan) { current.ScanSummary(rows) }
