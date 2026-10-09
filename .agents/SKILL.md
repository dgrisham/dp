---
name: dp
description: Use the dp CLI to filter, project, and render JSON or YAML from stdin — a small jq/nushell-like pipeline (where/get/select/length) with built-in table, JSON, and YAML output.
---

# dp

`dp` reads JSON or YAML from stdin, optionally runs it through a `-p`/`-pipeline` expression, and
prints the result as a table (interactive), JSON, or YAML.

```
some-command | dp [-p '<pipeline>'] [-to table|json|yaml] [-x] [-color always|never]
```

- `-p`, `-pipeline <expr>` — filter/project before rendering.
- `-to <format>` — `table`, `json`, or `yaml`. Default: table on a terminal, JSON when piped.
- `-x`, `-expand` — fully expand nested fields in tables at every depth (default: one level).
- `-color always|never` — default: auto (terminal + `NO_COLOR` unset).

## Pipeline grammar

Stages are separated by `|`.

- **`get <path>`** — project/drill into a field. Dot paths descend into objects by key and into
  lists by numeric index (negative counts from the end). A path segment that isn't a valid index on
  a list is instead mapped across every element, so both `0.id` (index then field) and `id.0`
  (field then index) work. `.` alone means the item itself.
- **`where <expr>`** — filters a list. Operators: `== != < > <= >= =~ !~ in not-in starts-with
  ends-with`. Precedence (tightest to loosest): comparisons, `not`, `and`, `xor`, `or`; parens
  group. `in`/`not-in` take a bracketed list: `status in [active, pending]`.
- **`select <path>...`** — keeps only the named fields (dotted paths rebuild minimal nesting).
- **`length`** — item count of a list; errors on anything else.

## Examples

```
echo '[{"name":"a","age":31},{"name":"b","age":19}]' | dp -p 'where age > 30 | get name'
echo '{"items":[...]}' | dp -p 'get items | select name age' -to json
echo '[...]' | dp -p 'get items | length'
```

## Output conventions

- A bare scalar result prints unquoted, and a bare array of scalars flattens to one unquoted item
  per line — like `jq -r` — but **only** when `-to` wasn't passed explicitly. `-to json`/`-to yaml`
  are strict, lossless output for a real downstream consumer (e.g. piping into `jq`).
- Table view never truncates values and never reacts to terminal width.
