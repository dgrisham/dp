// Package table renders an already-decoded value as plain-text tables.
//
// Column widths are purely content-derived and never read the terminal width, so output is
// identical no matter where you run it. A list of objects renders as a grid; a single object as a
// two-column key/value table. Nested values expand one level deep by default, relative to
// whatever's being rendered in this call (not the original document's absolute nesting) — e.g.
// `get parent` expands parent's own fields including a nested "children" list, but children's own
// nested fields collapse to a placeholder; `get parent.children` expands those instead. Pass
// expand=true (--expand/-x) to remove the depth cap entirely.
package table

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const unlimitedDepth = -1

func expandAt(depth int) bool {
	return depth != 0
}

func nextDepth(depth int) int {
	if depth < 0 {
		return depth
	}
	return depth - 1
}

// Render renders v as a table; ok is false when v isn't table-shaped (a bare scalar, or a
// top-level array of non-objects) and the caller should fall back to JSON.
func Render(v any) (out string, ok bool) {
	return RenderOrdered(v, nil, false)
}

// RenderOrdered is Render, but columns/fields named in order are shown first (e.g. from a
// pipeline `select a b`, since plain maps can't preserve that order on their own).
func RenderOrdered(v any, order []string, expand bool) (out string, ok bool) {
	depth := 1
	if expand {
		depth = unlimitedDepth
	}
	switch t := v.(type) {
	case []any:
		return renderArray(t, order, depth)
	case map[string]any:
		return strings.Join(renderRecordLines(t, order, depth), "\n"), true
	default:
		return "", false
	}
}

func renderArray(items []any, order []string, depth int) (string, bool) {
	if len(items) == 0 {
		return "[]", true
	}
	rows := make([]map[string]any, len(items))
	for i, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return "", false
		}
		rows[i] = m
	}
	return strings.Join(renderListLines(rows, order, depth), "\n"), true
}

// orderColumns puts cols named in preferred first, then the rest alphabetically.
func orderColumns(cols []string, preferred []string) []string {
	if len(preferred) == 0 {
		sort.Strings(cols)
		return cols
	}
	present := make(map[string]bool, len(cols))
	for _, c := range cols {
		present[c] = true
	}
	seen := make(map[string]bool, len(cols))
	ordered := make([]string, 0, len(cols))
	for _, p := range preferred {
		if present[p] && !seen[p] {
			ordered = append(ordered, p)
			seen[p] = true
		}
	}
	var rest []string
	for _, c := range cols {
		if !seen[c] {
			rest = append(rest, c)
		}
	}
	sort.Strings(rest)
	return append(ordered, rest...)
}

// renderListLines draws a bordered grid. A cell spanning multiple lines (see renderValueLines)
// grows its whole row to match, blank-padding the other cells.
func renderListLines(rows []map[string]any, order []string, depth int) []string {
	colSet := map[string]bool{}
	for _, r := range rows {
		for k := range r {
			colSet[k] = true
		}
	}
	cols := make([]string, 0, len(colSet))
	for k := range colSet {
		cols = append(cols, k)
	}
	cols = orderColumns(cols, order)

	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len([]rune(c))
	}
	cellLines := make([][][]string, len(rows))
	for i, r := range rows {
		cellLines[i] = make([][]string, len(cols))
		for j, c := range cols {
			lines := renderValueLines(r[c], depth)
			cellLines[i][j] = lines
			for _, l := range lines {
				if w := len([]rune(l)); w > widths[j] {
					widths[j] = w
				}
			}
		}
	}

	var lines []string
	lines = append(lines, borderLine(widths, '┌', '┬', '┐'))
	lines = append(lines, dataRow(cols, widths))
	lines = append(lines, borderLine(widths, '├', '┼', '┤'))
	for _, row := range cellLines {
		height := 1
		for _, cell := range row {
			if len(cell) > height {
				height = len(cell)
			}
		}
		for li := 0; li < height; li++ {
			vals := make([]string, len(cols))
			for j, cell := range row {
				if li < len(cell) {
					vals[j] = cell[li]
				}
			}
			lines = append(lines, dataRow(vals, widths))
		}
	}
	lines = append(lines, borderLine(widths, '└', '┴', '┘'))
	return lines
}

func borderLine(widths []int, left, mid, right rune) string {
	var b strings.Builder
	b.WriteRune(left)
	for i, w := range widths {
		b.WriteString(strings.Repeat("─", w+2))
		if i < len(widths)-1 {
			b.WriteRune(mid)
		}
	}
	b.WriteRune(right)
	return b.String()
}

func dataRow(vals []string, widths []int) string {
	var b strings.Builder
	b.WriteRune('│')
	for i, v := range vals {
		b.WriteByte(' ')
		b.WriteString(v)
		b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(v))))
		b.WriteByte(' ')
		b.WriteRune('│')
	}
	return b.String()
}

func renderScalarListLines(items []any) []string {
	cells := make([]string, len(items))
	width := 0
	for i, it := range items {
		cells[i] = formatScalar(it)
		if w := len([]rune(cells[i])); w > width {
			width = w
		}
	}
	lines := make([]string, 0, len(items)+2)
	lines = append(lines, "┌"+strings.Repeat("─", width+2)+"┐")
	for _, c := range cells {
		lines = append(lines, "│ "+c+strings.Repeat(" ", width-len([]rune(c)))+" │")
	}
	lines = append(lines, "└"+strings.Repeat("─", width+2)+"┘")
	return lines
}

func formatScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return formatNumber(t)
	default:
		return fmt.Sprint(t)
	}
}

// formatNumber avoids Go's %g scientific notation for large whole numbers.
func formatNumber(f float64) string {
	if f == math.Trunc(f) && !math.IsInf(f, 0) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func renderRecordLines(m map[string]any, order []string, depth int) []string {
	if len(m) == 0 {
		return []string{"{}"}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	keys = orderColumns(keys, order)

	keyWidth := 0
	for _, k := range keys {
		if len(k) > keyWidth {
			keyWidth = len(k)
		}
	}

	type field struct {
		key   string
		lines []string
	}
	fields := make([]field, len(keys))
	valWidth := 0
	for i, k := range keys {
		lines := renderValueLines(m[k], depth)
		fields[i] = field{k, lines}
		for _, l := range lines {
			if w := len([]rune(l)); w > valWidth {
				valWidth = w
			}
		}
	}

	var out []string
	out = append(out, "┌"+strings.Repeat("─", keyWidth+2)+"┬"+strings.Repeat("─", valWidth+2)+"┐")
	for _, f := range fields {
		for j, l := range f.lines {
			key := ""
			if j == 0 {
				key = f.key
			}
			out = append(out, fmt.Sprintf("│ %-*s │ %-*s │", keyWidth, key, valWidth, l))
		}
	}
	out = append(out, "└"+strings.Repeat("─", keyWidth+2)+"┴"+strings.Repeat("─", valWidth+2)+"┘")
	return out
}

// renderValueLines renders one cell: a scalar is one line in full; an empty object/array is
// "{}"/"[]"; a non-empty one expands into its own nested box only if expandAt(depth), else
// collapses to a placeholder like "{3 fields}".
func renderValueLines(v any, depth int) []string {
	switch t := v.(type) {
	case nil:
		return []string{""}
	case map[string]any:
		if len(t) == 0 {
			return []string{"{}"}
		}
		if !expandAt(depth) {
			return []string{fmt.Sprintf("{%d fields}", len(t))}
		}
		return renderRecordLines(t, nil, nextDepth(depth))
	case []any:
		if len(t) == 0 {
			return []string{"[]"}
		}
		if !expandAt(depth) {
			return []string{fmt.Sprintf("[%d items]", len(t))}
		}
		rows := make([]map[string]any, len(t))
		allObjects := true
		for i, it := range t {
			m, ok := it.(map[string]any)
			if !ok {
				allObjects = false
				break
			}
			rows[i] = m
		}
		if allObjects {
			return renderListLines(rows, nil, nextDepth(depth))
		}
		return renderScalarListLines(t)
	default:
		return []string{formatScalar(t)}
	}
}
