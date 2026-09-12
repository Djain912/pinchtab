# Extract Architecture

`internal/extract` fills a flat JSON schema against a captured accessibility
snapshot. It sits between `find` (one element) and `scrape` (a whole site): an
agent hands over a schema and gets typed data back, with per-field confidence so
it can fall back to `find` where a field is uncertain.

It is model-free and browserless. It reuses the same semantic matcher that backs
`find` (`github.com/pinchtab/semantic`, lexical + feature-hash embedding, no
network, no model download) over a node list it is handed. It never touches CDP.

## Pipeline

```text
JSON schema ──ParseSchema──► Schema (ordered properties, resolved hints)
node list   ──canonical order (by ref)──► descriptors (semdesc.Build)
                                    │
        per property: build query ──► matcher.Find (TopK 1) ──► best ref + score
                                    │
                read value (Value|Text|Name, Checked for bool) ──► coerce to type
                                    │
                    data + per-field {ref, score, confidence, source, reason}
```

## Schema subset

The accepted schema is an `object` with `properties`. Each property is one of
`string`, `number`, `integer`, `boolean`, or `array`. Per property the extractor
honours:

- `description` — matched alongside the property name as query text.
- `required` — a required field that cannot be filled is listed in `missing`,
  never invented.
- `x-pinchtab-hint` — an explicit query or selector that overrides the
  name-based query (see below).

`ParseSchema` returns a typed `*UnsupportedError` naming the offending path for
constructs outside the subset:

- a non-`object` root (`type: array is not supported`),
- a property typed `object` (`properties.price.type: object is not supported`),
- an unknown type (`properties.price.type: decimal is not supported`),
- nested `properties` on a property (`properties.price.properties: ...`),
- a `css:` or `xpath:` hint, which needs a browser
  (`properties.price.x-pinchtab-hint: ...`).

`array` parses successfully but resolves to a missing field with reason
`unsupported`; array-of-object resolution is a follow-up task.

Property order is alphabetical, so `missing` and iteration are deterministic.

## Field resolution

For each property the extractor builds a query and takes the single best node
above the threshold (default `0.3`, same as `find`):

1. **Query.** A resolved `x-pinchtab-hint` wins. Otherwise the query is the
   property name joined with its description.
2. **Match.** The query runs through the shared combined matcher against the
   descriptors. `best_ref` and `score` come straight from the matcher.
3. **Read.** The matched node's value is read in priority order `Value`, `Text`,
   `Name`; booleans consult the accessibility `Checked` state first.
4. **Coerce.** The raw string is coerced to the schema type. A coercion failure
   leaves the field missing with a reason — never a wrong-typed value.

### Hints

`x-pinchtab-hint` accepts either a bare query or a selector, restricted to the
kinds a node list can answer without a browser:

- a bare string → used verbatim as the query,
- `find:` → the natural-language query,
- `role:`, `label:`, `placeholder:`, `alt:`, `title:`, `testid:` → routed
  through `selector.SemanticQuery` so the grammar stays single-sourced with the
  action and `find` paths,
- `text:` → the text as a query,
- `ref:` (or a bare `eN`) → selects that node verbatim; a ref that is not in the
  node list leaves the field missing with reason `ref_not_found`,
- `css:` / `xpath:` → rejected by `ParseSchema` (they need a live DOM).

A hint that resolves beats the name-based query, so an agent can pin an ambiguous
field (e.g. a sale price among several prices) without renaming the schema.

## Coercion rules

- **string** — the trimmed raw value.
- **number / integer** — only the **first numeric token** is parsed: a leading
  currency symbol and sign are stripped, the token's thousands separators are
  dropped, sign and decimal are kept, and parsing stops at the first character
  after the token so digits from a trailing word are never glued on. Both ASCII
  `-` and the Unicode minus `−` (U+2212) are honoured. `integer` truncates toward
  zero. Examples: `"$1,299.00"` → `1299`, `"−3.5 kg"` → `-3.5`,
  `"4.7 out of 5"` → `4.7` (not `4.75`), `"2 of 3"` → `2`, `"call for price"` →
  missing with reason `not_numeric`.
- **boolean** — the accessibility `Checked` state (`true`/`false`) first, then
  the words `yes`/`no`/`true`/`false` in the read value. A `mixed` checkbox or an
  unrelated string leaves the field missing with reason `not_boolean`.

## Confidence

Each field carries `ref`, `score`, and `confidence`. The confidence band comes
from `semantic.CalibrateConfidence` — `high` (score ≥ 0.8), `medium` (≥ 0.6),
`low` (below) — the same bands `find` reports, so the two cannot drift. An agent
can fall back to `find` on a `low` field.

## Determinism

`Resolve` is deterministic. Before matching, the node list is copied and stably
sorted into document order by ref (snapshot refs `e1`, `e2`, … are assigned in
pre-order, so numeric ref order is document order). The same schema over the same
nodes — in any input order — yields identical output; ties break on document
order.

## Descriptors

Nodes are converted to matcher descriptors by `internal/semdesc`, the single
source of the node-to-descriptor mapping shared with the `find` handler. The
extractor has no browser, so it skips the DOM-metadata enrichment `find` applies
to live snapshots and matches the node list as handed over.
