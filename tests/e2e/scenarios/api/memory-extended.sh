#!/bin/bash
# memory-extended.sh — the memory capability refusal on a server whose config leaves
# security.allowMemory off (pinchtab-secure). memory-basic.sh runs the enabled path on
# the default server; the basic lane has one server, so the second config lives here.

GROUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${GROUP_DIR}/../../helpers/api.sh"

memory_disabled_tests() {
  # ─────────────────────────────────────────────────────────────────
  start_test "memory: allowMemory off refuses snapshot and summary, usage still answers"

  pt_post /navigate -d "{\"url\":\"${FIXTURES_URL}/memory-leak.html\"}"
  assert_ok "navigate to memory-leak.html"
  TAB_ID=$(echo "$RESULT" | jq -r '.tabId')

  pt_get "/memory?tabId=${TAB_ID}"
  assert_ok "GET /memory is not capability-gated"
  assert_json_exists "$RESULT" '.usedJSHeapSize' "usage reports usedJSHeapSize"

  pt_post /memory/snapshot -d "{\"tabId\":\"${TAB_ID}\"}"
  assert_http_status 403 "snapshot refused"
  assert_json_eq "$RESULT" '.code' 'memory_disabled' "snapshot answers memory_disabled"
  assert_json_eq "$RESULT" '.details.setting' 'security.allowMemory' "refusal names the setting"

  pt_get "/memory/snapshot/heap_any/summary"
  assert_http_status 403 "summary refused"
  assert_json_eq "$RESULT" '.code' 'memory_disabled' "summary answers memory_disabled"

  end_test
}

with_server "$E2E_SECURE_SERVER" memory_disabled_tests
