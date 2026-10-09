// Package render turns an already-decoded value into table/json/yaml text, optionally filtering
// or projecting it through a query.Run pipeline first.
//
// This package makes no terminal/color/default-format decisions of its own — the caller resolves
// those and passes them in as Options.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dgrisham/dp/query"
	"github.com/dgrisham/dp/table"
)

// Options controls how Print renders a value.
type Options struct {
	// Pipeline is a query.Run expression applied to the value before rendering; "" skips it.
	Pipeline string

	// Format is "table", "json", or "yaml".
	Format string

	// Expand disables table rendering's one-level default cap on nested expansion.
	Expand bool

	// Color colorizes JSON/indented output via `jq -C` when installed (only on the non-table,
	// non-yaml fallback path).
	Color bool

	// Raw enables jq -r-style conveniences: a bare scalar prints unquoted, and a bare array of
	// scalars flattens to one unquoted item per line, instead of being JSON-encoded. Appropriate
	// when the caller hasn't been asked for strict, lossless output.
	Raw bool
}

// Print applies opts.Pipeline to v (if set), then writes the result to w per the rest of opts.
// defaultOrder names fields/columns to show first in table view, overridden by a pipeline whose
// last stage is a `select`. Pass nil if there's no default order.
func Print(w io.Writer, v any, defaultOrder []string, opts Options) error {
	columnOrder := defaultOrder
	if opts.Pipeline != "" {
		result, order, err := query.Run(opts.Pipeline, v)
		if err != nil {
			return fmt.Errorf("pipeline: %w", err)
		}
		if order != nil {
			columnOrder = order
		}
		v = result
	}

	switch opts.Format {
	case "table":
		if out, ok := table.RenderOrdered(v, columnOrder, opts.Expand); ok {
			fmt.Fprintln(w, out)
			return nil
		}
	case "yaml":
		if data, err := yaml.Marshal(v); err == nil {
			fmt.Fprintln(w, strings.TrimRight(string(data), "\n"))
			return nil
		}
	}

	if opts.Raw {
		if arr, ok := v.([]any); ok && isScalarList(arr) {
			for _, item := range arr {
				fmt.Fprintln(w, scalarLine(item))
			}
			return nil
		}
		if s, ok := v.(string); ok {
			fmt.Fprintln(w, s)
			return nil
		}
	}

	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshaling result: %w", err)
	}
	if opts.Color {
		if colored, ok := colorizeWithJQ(raw); ok {
			fmt.Fprintln(w, colored)
			return nil
		}
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		fmt.Fprintln(w, strings.TrimRight(string(raw), "\n"))
		return nil
	}
	fmt.Fprintln(w, strings.TrimRight(buf.String(), "\n"))
	return nil
}

func formatNumber(f float64) string {
	if f == math.Trunc(f) && !math.IsInf(f, 0) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// scalarLine matches jq -r's unquoted style.
func scalarLine(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
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

// isScalarList is vacuously true for an empty list, so an empty result flattens to zero lines
// instead of a literal "[]" a shell loop would iterate over once.
func isScalarList(items []any) bool {
	for _, it := range items {
		switch it.(type) {
		case map[string]any, []any:
			return false
		}
	}
	return true
}

func colorizeWithJQ(raw json.RawMessage) (string, bool) {
	jqPath, err := exec.LookPath("jq")
	if err != nil {
		return "", false
	}
	cmd := exec.Command(jqPath, "-C", ".")
	cmd.Stdin = bytes.NewReader(raw)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return strings.TrimRight(out.String(), "\n"), true
}
