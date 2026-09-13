#!/bin/bash
# extract-corpus-extended.sh — the live /extract pipeline on each mirrored corpus page scores at least the offline unit corpus.

GROUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${GROUP_DIR}/../../helpers/api.sh"

CORPUS_DIR="${GROUP_DIR}/../../fixtures/corpus"
CORPUS_MANIFEST="${CORPUS_DIR}/manifest.json"

CORPUS_OUTCOMES='
def outcomes($got; $want; $prefix):
  $want | to_entries[] | .key as $k | .value as $w |
  if ($w | type) == "array" then
    (($got // {})[$k] // []) as $g |
    range(0; [($w | length), ($g | length)] | max) as $i |
    if $i >= ($w | length) then
      {path: "\($prefix)\($k)[\($i)]", hit: false, got: $g[$i], want: null}
    else
      outcomes($g[$i]; $w[$i]; "\($prefix)\($k)[\($i)].")
    end
  else
    {path: "\($prefix)\($k)", hit: ((($got // {})[$k]) == $w), got: (($got // {})[$k]), want: $w}
  end;
[outcomes($got; $want; "")]
'

# ─────────────────────────────────────────────────────────────────
start_test "extract-corpus: live render, snapshot and resolve score at least the offline corpus"

for NAME in $(jq -r '.entries | keys[]' "$CORPUS_MANIFEST"); do
  OFFLINE_REASON=$(jq -r --arg n "$NAME" '.entries[$n].offline // empty' "$CORPUS_MANIFEST")
  if [ -n "$OFFLINE_REASON" ]; then
    skip_assert "${NAME}: offline-only, skipped: ${OFFLINE_REASON}"
    continue
  fi
  MIN_HITS=$(jq -r --arg n "$NAME" '.entries[$n].minHits' "$CORPUS_MANIFEST")

  pt_post /navigate -d "{\"url\":\"${FIXTURES_URL}/corpus/${NAME}.html\"}" >/dev/null
  assert_ok "${NAME}: navigate to corpus/${NAME}.html"

  SCHEMA=$(jq -c . "${CORPUS_DIR}/${NAME}.schema.json")
  pt_post /extract -d "{\"schema\":${SCHEMA}}" >/dev/null
  assert_ok "${NAME}: extract"

  OUTCOMES=$(echo "$RESULT" | jq -c --slurpfile want "${CORPUS_DIR}/${NAME}.expected.json" \
    '.data as $got | $want[0] as $want | '"$CORPUS_OUTCOMES")
  echo "$OUTCOMES" | jq -r '.[] | "    \(if .hit then "hit " else "miss" end) \(.path) got=\(.got | tojson) want=\(.want | tojson)" | .[0:160]'
  HITS=$(echo "$OUTCOMES" | jq '[.[] | select(.hit)] | length')
  TOTAL=$(echo "$OUTCOMES" | jq 'length')

  if [ "$HITS" -ge "$MIN_HITS" ]; then
    pass_assert "${NAME}: pass, ${HITS}/${TOTAL} fields match expected.json (offline floor ${MIN_HITS})"
  else
    fail_assert "${NAME}: fail, ${HITS}/${TOTAL} fields match expected.json, below the offline floor ${MIN_HITS}"
  fi
done

end_test
