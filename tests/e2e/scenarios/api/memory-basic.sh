#!/bin/bash
# memory-basic.sh — GET /memory tracks a leak and its release; POST /memory/snapshot
# writes a heap snapshot the summary endpoint reads. The default e2e server runs with
# security.allowMemory on; memory-extended.sh covers the refusal on a server with it off.

GROUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${GROUP_DIR}/../../helpers/api.sh"

MB=$((1024 * 1024))

# ─────────────────────────────────────────────────────────────────
start_test "memory: usedJSHeapSize grows with three leaks and falls back after release + gc"

pt_post /navigate -d "{\"url\":\"${FIXTURES_URL}/memory-leak.html\"}"
assert_ok "navigate to memory-leak.html"
TAB_ID=$(echo "$RESULT" | jq -r '.tabId')

pt_get "/memory?tabId=${TAB_ID}&gc=true"
assert_ok "baseline read with gc"
for key in usedJSHeapSize totalJSHeapSize jsHeapSizeLimit documents nodes listeners frames; do
  assert_json_exists "$RESULT" ".${key}" "usage reports ${key}"
done
BASELINE=$(echo "$RESULT" | jq -r '.usedJSHeapSize')

for i in 1 2 3; do
  pt_post /action -d "{\"tabId\":\"${TAB_ID}\",\"kind\":\"click\",\"selector\":\"#leak\"}"
  assert_ok "leak click ${i}"
done

pt_get "/memory?tabId=${TAB_ID}"
assert_ok "read after three leaks"
LEAKED=$(echo "$RESULT" | jq -r '.usedJSHeapSize')
if [ "$LEAKED" -ge $((BASELINE + 10 * MB)) ]; then
  pass_assert "usedJSHeapSize grew by at least 10 MB ($BASELINE → $LEAKED)"
else
  fail_assert "usedJSHeapSize grew only from $BASELINE to $LEAKED, want at least +10 MB"
fi

pt_post /action -d "{\"tabId\":\"${TAB_ID}\",\"kind\":\"click\",\"selector\":\"#release\"}"
assert_ok "release click"

pt_get "/memory?tabId=${TAB_ID}&gc=true"
assert_ok "read after release with gc"
assert_json_eq "$RESULT" '.gc' 'true' "reading says it collected first"
RELEASED=$(echo "$RESULT" | jq -r '.usedJSHeapSize')
if [ "$RELEASED" -lt $((BASELINE + 2 * MB)) ]; then
  pass_assert "usedJSHeapSize back under baseline + 2 MB ($RELEASED < $BASELINE + 2 MB)"
else
  fail_assert "usedJSHeapSize $RELEASED after release + gc, want under $((BASELINE + 2 * MB))"
fi

pt_get "/tabs/${TAB_ID}/memory"
assert_ok "tab-scoped usage route"
assert_json_eq "$RESULT" '.tabId' "$TAB_ID" "tab-scoped read names the tab"

end_test

# ─────────────────────────────────────────────────────────────────
start_test "memory: snapshot writes a server-side file whose summary lists Array"

pt_post /action -d "{\"tabId\":\"${TAB_ID}\",\"kind\":\"click\",\"selector\":\"#leak\"}"
assert_ok "leak one array so the snapshot holds it"

pt_post /memory/snapshot -d "{\"tabId\":\"${TAB_ID}\"}"
assert_ok "take heap snapshot"
SNAP_ID=$(echo "$RESULT" | jq -r '.id')
assert_json_contains "$RESULT" '.path' '/heapsnapshots/' "path is under the server-controlled heapsnapshots dir"
assert_json_contains "$RESULT" '.path' '.heapsnapshot' "path carries the .heapsnapshot extension"
assert_json_jq "$RESULT" '.bytes > 0' "snapshot has bytes" "snapshot reported no bytes"
assert_json_jq "$RESULT" '.nodeCount > 0' "snapshot reports a node count" "snapshot reported no nodes"
assert_json_exists "$RESULT" '.durationMs' "snapshot reports its duration"

pt_get "/memory/snapshot/${SNAP_ID}/summary?top=50"
assert_ok "summary reads the file the snapshot wrote"
assert_json_jq "$RESULT" '.nodeCount > 0 and .edgeCount > 0' "summary counts nodes and edges" "summary counted no nodes or edges"
assert_json_jq "$RESULT" '([.topBySize[].name] + [.topByCount[].name]) | index("Array") != null' "Array is among the top constructors" "Array missing from the top constructors"
assert_json_exists "$RESULT" '.duplicateStrings' "summary lists duplicate strings"

pt_get "/memory/snapshot/heap_does_not_exist/summary"
assert_http_status 404 "unknown snapshot id"
assert_json_eq "$RESULT" '.code' 'memory_snapshot_not_found' "unknown id is memory_snapshot_not_found"

pt_post /action -d "{\"tabId\":\"${TAB_ID}\",\"kind\":\"click\",\"selector\":\"#release\"}"

end_test
