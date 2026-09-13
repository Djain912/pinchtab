#!/bin/bash
# memory-basic.sh — CLI: pinchtab memory reads heap usage, memory snapshot writes a
# heap snapshot, and memory summary prints its constructor table. The default e2e
# server runs with security.allowMemory on.

GROUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${GROUP_DIR}/../../helpers/cli.sh"

# ─────────────────────────────────────────────────────────────────
start_test "pinchtab memory --json"

pt_ok nav "${FIXTURES_URL}/memory-leak.html"

pt_ok memory --json
for key in usedJSHeapSize totalJSHeapSize jsHeapSizeLimit documents nodes listeners frames; do
  assert_output_contains "\"$key\"" "usage reports $key"
done

pt_ok memory --gc
assert_output_contains "heap used" "human summary line"
assert_output_contains "(after gc)" "summary says it collected first"

end_test

# ─────────────────────────────────────────────────────────────────
start_test "pinchtab memory snapshot + summary --top 5"

pt_ok click "#leak"

pt_ok memory snapshot
assert_output_contains "Heap snapshot heap_" "snapshot prints its id"
assert_output_contains ".heapsnapshot" "snapshot prints the file path"
SNAP_ID=$(grep -o 'pinchtab memory summary [^ ]*' <<<"$PT_OUT" | awk '{print $4}')
if [ -n "$SNAP_ID" ]; then
  pass_assert "snapshot id parsed: $SNAP_ID"
else
  fail_assert "no snapshot id in the output"
fi

pt_ok memory summary "$SNAP_ID" --top 5
assert_output_contains "CONSTRUCTOR" "summary prints the constructor table header"
assert_output_contains "Top constructors by self size" "size table present"
assert_output_contains "Top constructors by count" "count table present"

pt_ok memory summary "$SNAP_ID" --top 5 --json
assert_output_contains '"topBySize"' "json summary carries topBySize"

pt_ok click "#release"

end_test
