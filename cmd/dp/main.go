// Command dp is a small standalone CLI around the query/render packages: pipe JSON or YAML in,
// get a filtered/projected table (or JSON/YAML) out.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/dgrisham/dp/render"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run() error {
	var pipeline, format, color string
	var expand bool
	flag.StringVar(&pipeline, "pipeline", "", `Filter/project the input before rendering, e.g. 'where age > 30 | get name'`)
	flag.StringVar(&pipeline, "p", "", "Shorthand for -pipeline")
	flag.StringVar(&format, "to", "", "Output format: table, json, or yaml (default: table when interactive, json when piped)")
	flag.BoolVar(&expand, "expand", false, "Fully expand nested fields in tables, at every depth (default: one level)")
	flag.BoolVar(&expand, "x", false, "Shorthand for -expand")
	flag.StringVar(&color, "color", "", "always, never, or auto (default: auto — a terminal, unless NO_COLOR is set)")
	flag.Parse()

	switch format {
	case "", "table", "json", "yaml":
	default:
		return fmt.Errorf("invalid -to value %q: must be table, json, or yaml", format)
	}
	explicitFormat := format != ""

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	v, err := decode(input)
	if err != nil {
		return fmt.Errorf("parsing input as JSON or YAML: %w", err)
	}

	isTerminal := term.IsTerminal(int(os.Stdout.Fd()))

	resolvedFormat := format
	if resolvedFormat == "" {
		if isTerminal {
			resolvedFormat = "table"
		} else {
			resolvedFormat = "json"
		}
	}

	wantColor := isTerminal && os.Getenv("NO_COLOR") == ""
	switch color {
	case "always":
		wantColor = true
	case "never":
		wantColor = false
	}

	opts := render.Options{
		Pipeline: pipeline,
		Format:   resolvedFormat,
		Expand:   expand,
		Color:    wantColor,
		Raw:      !explicitFormat, // only the auto-detected default gets jq -r-style conveniences
	}
	return render.Print(os.Stdout, v, nil, opts)
}

// decode tries JSON first, falling back to YAML. A '{'/'['-led document is treated as committed
// to JSON: on failure that error is returned directly, since YAML's flow grammar can
// "successfully" reinterpret malformed JSON as something silently wrong instead of erroring.
func decode(data []byte) (any, error) {
	var v any
	jsonErr := json.Unmarshal(data, &v)
	if jsonErr == nil {
		return v, nil
	}
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return nil, jsonErr
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return normalizeNumbers(v), nil
}

func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalizeNumbers(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = normalizeNumbers(val)
		}
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	default:
		return v
	}
}
