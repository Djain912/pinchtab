#!/bin/bash
# text-markdown-basic.sh — GET /text?mode=markdown: page-to-Markdown conversion,
# format/content-type, line-boundary truncation, frame scope, refusal, and the
# IDPI guard parity with the readability path.

GROUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${GROUP_DIR}/../../helpers/api.sh"

# ─────────────────────────────────────────────────────────────────
start_test "markdown: GET /text?mode=markdown returns converter Markdown"

pt_post /navigate -d "{\"url\":\"${FIXTURES_URL}/markdown.html\"}"
assert_ok "navigate to markdown.html"

pt_get "/text?mode=markdown"
assert_ok "text markdown"
assert_json_eq "$RESULT" '.extraction' 'markdown' "extraction echoes markdown"

TITLE=$(echo "$RESULT" | jq -r '.title')
if [ -n "$TITLE" ] && [ "$TITLE" != "null" ]; then
  echo -e "  ${GREEN}✓${NC} title present ($TITLE)"
  ((ASSERTIONS_PASSED++)) || true
else
  echo -e "  ${RED}✗${NC} title missing"
  ((ASSERTIONS_FAILED++)) || true
fi

BODY=$(echo "$RESULT" | jq -r '.text')
assert_contains "$BODY" "# " "heading survives as Markdown"
assert_contains "$BODY" "## " "subheading survives as Markdown"
assert_contains "$BODY" "](https://example.com/link)" "inline link survives as Markdown"
assert_contains "$BODY" "- " "list item survives as Markdown"
assert_contains "$BODY" "|" "table survives as Markdown"

end_test

# ─────────────────────────────────────────────────────────────────
start_test "markdown: format=text returns text/markdown content type"

RESPONSE=$(e2e_curl -s -w "\n%{http_code}\n%{content_type}" "${E2E_SERVER}/text?mode=markdown&format=text")
STATUS=$(echo "$RESPONSE" | tail -n 2 | head -1)
CTYPE=$(echo "$RESPONSE" | tail -n 1)

HTTP_STATUS="$STATUS"; assert_http_status 200 "format=text returned 200"
if echo "$CTYPE" | grep -q "text/markdown"; then
  echo -e "  ${GREEN}✓${NC} content-type is text/markdown"
  ((ASSERTIONS_PASSED++)) || true
else
  echo -e "  ${RED}✗${NC} content-type is $CTYPE (expected text/markdown)"
  ((ASSERTIONS_FAILED++)) || true
fi

end_test

# ─────────────────────────────────────────────────────────────────
start_test "markdown: maxChars truncates on a line boundary"

pt_get "/text?mode=markdown&maxChars=120"
assert_ok "text markdown maxChars=120"
assert_json_eq "$RESULT" '.truncated' 'true' "response marked truncated"

# The last line must be whole: no dangling half of a table row or link. Every
# returned line has to appear verbatim in the untruncated body.
FULL=$(pt_get "/text?mode=markdown" >/dev/null; echo "$RESULT" | jq -r '.text')
CUT=$(pt_get "/text?mode=markdown&maxChars=120" >/dev/null; echo "$RESULT" | jq -r '.text')
PARTIAL_LINE=0
while IFS= read -r line; do
  [ -z "$line" ] && continue
  if ! grep -Fqx -- "$line" <<<"$FULL"; then
    PARTIAL_LINE=1
    echo -e "  ${RED}✗${NC} line not whole: $line"
  fi
done <<<"$CUT"
if [ "$PARTIAL_LINE" -eq 0 ]; then
  echo -e "  ${GREEN}✓${NC} every truncated line is a complete source line"
  ((ASSERTIONS_PASSED++)) || true
else
  ((ASSERTIONS_FAILED++)) || true
fi

end_test

# ─────────────────────────────────────────────────────────────────
start_test "markdown: frame scope converts the selected iframe"

pt_post /frame -d '{"target":"#md-frame"}'
assert_ok "select iframe scope"
assert_json_eq "$RESULT" '.scoped' 'true' "frame scope enabled"

pt_get "/text?mode=markdown"
assert_ok "frame-scoped markdown"
FRAME_BODY=$(echo "$RESULT" | jq -r '.text')
assert_contains "$FRAME_BODY" "Card number" "iframe document converted, not the parent"
assert_not_contains "$FRAME_BODY" "Markdown Fixture" "parent article excluded from frame scope"

pt_post /frame -d '{"target":"main"}'
assert_ok "reset frame scope"

end_test

# ─────────────────────────────────────────────────────────────────
start_test "markdown: unknown mode is refused with a 400"

pt_get "/text?mode=bogus"
assert_http_status 400 "unknown mode refused"

end_test

# ─────────────────────────────────────────────────────────────────
start_test "markdown: IDPI guard applies as on the readability path"

pt_post /navigate -d "{\"url\":\"${FIXTURES_URL}/idpi-inject.html\"}"
assert_ok "navigate to idpi-inject.html"

# Warn mode (default api service): the request is answered and the advisory
# rides on the X-IDPI-Warning header and the idpiWarning body field, identical
# to the readability path on the same page.
TMPH=$(mktemp)
RESPONSE=$(e2e_curl -s -w "\n%{http_code}" -D "$TMPH" "${E2E_SERVER}/text?mode=markdown")
MD_STATUS=$(echo "$RESPONSE" | tail -n 1)
MD_BODY=$(echo "$RESPONSE" | head -n -1)
MD_WARN_HDR=$(grep -i "^X-IDPI-Warning:" "$TMPH" | sed 's/^[^:]*: *//' | tr -d '\r' | head -1)
rm -f "$TMPH"

HTTP_STATUS="$MD_STATUS"; assert_http_status 200 "markdown returns in warn mode"
if [ -n "$MD_WARN_HDR" ]; then
  echo -e "  ${GREEN}✓${NC} X-IDPI-Warning header present"
  ((ASSERTIONS_PASSED++)) || true
else
  echo -e "  ${RED}✗${NC} X-IDPI-Warning header missing"
  ((ASSERTIONS_FAILED++)) || true
fi
assert_json_exists "$MD_BODY" ".idpiWarning" "idpiWarning field in markdown body"

# Parity: the readability path warns on the same page.
TMPH2=$(mktemp)
e2e_curl -s -D "$TMPH2" "${E2E_SERVER}/text" >/dev/null
RD_WARN_HDR=$(grep -i "^X-IDPI-Warning:" "$TMPH2" | sed 's/^[^:]*: *//' | tr -d '\r' | head -1)
rm -f "$TMPH2"
if [ -n "$RD_WARN_HDR" ]; then
  echo -e "  ${GREEN}✓${NC} readability path warns identically on the same page"
  ((ASSERTIONS_PASSED++)) || true
else
  echo -e "  ${RED}✗${NC} readability path did not warn; guard parity broken"
  ((ASSERTIONS_FAILED++)) || true
fi

end_test
