# dp

A standalone CLI: pipe JSON or YAML into it, filter/project it with a small jq/nushell-like
pipeline, and render the result as a table, JSON, or YAML.

## Layout

- `query/` — the pipeline language (`where`, `get`, `select`, `length`). Pure: operates on an
  already-decoded `map[string]any`/`[]any`/scalar value, no I/O.
- `table/` — renders a decoded value as a plain-text table. Pure, same input shape.
- `render/` — ties `query` + `table` together behind `Print(w, v, defaultOrder, Options)`. Makes
  no terminal/color/default-format decisions itself — `Options` carries the caller's resolved
  choices.
- `cmd/dp/` — the CLI: reads stdin, decodes JSON (falling back to YAML), resolves flags into
  `render.Options`, calls `render.Print`.

## Design constraints worth knowing before changing things

- **No terminal-width dependence anywhere in `table/`.** Column widths are purely content-derived;
  values are never truncated. This is deliberate — don't reintroduce `$COLUMNS`-based sizing.
- **Nested expansion in tables is depth-relative to the call, not the document.** One level expands
  by default; `--expand` removes the cap. See `table`'s package doc before touching
  `expandAt`/`nextDepth`.
- **`query` and `render` take no env vars and do no isatty detection.** `cmd/dp` is the only place
  that resolves "what format", "what color", "is this piped" — keep it that way so these packages
  stay embeddable elsewhere.
- **JSON input that looks JSON-shaped (`{`/`[`-led) never silently falls back to YAML on a parse
  error.** YAML's flow grammar is lenient enough to "successfully" reinterpret malformed JSON as
  something silently wrong. See `decode` in `cmd/dp/main.go`.
- **Comments are kept minimal.** Only for non-obvious invariants (e.g. why a numeric-but-out-of-
  range path segment must hard-fail instead of falling through to field-name mapping). Don't restate
  what the code already says.

## Build / run

```
go build ./...
go install ./cmd/dp
echo '[{"name":"a","age":31}]' | dp -p 'where age > 30 | get name'
```

No external services, no credentials, no network calls (other than optionally shelling out to a
locally-installed `jq` for colorized JSON output).
