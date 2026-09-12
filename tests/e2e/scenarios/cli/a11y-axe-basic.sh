#!/bin/bash
# a11y-axe-basic.sh — CLI: pinchtab a11y audit --axe yields the same violation
# ids as the API, and --tags filters the run.

GROUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${GROUP_DIR}/../../helpers/cli.sh"

# ─────────────────────────────────────────────────────────────────
start_test "pinchtab a11y audit --axe --json"

pt_ok nav "${FIXTURES_URL}/a11y-violations.html"

pt_ok a11y audit --axe --json
assert_output_contains '"engine": "axe"' "engine echoes axe"
assert_output_contains '"version": "4.13.0"' "axe version echoed"
for want in image-alt label color-contrast; do
  assert_output_contains "\"$want\"" "violation $want present"
done

end_test

# ─────────────────────────────────────────────────────────────────
start_test "pinchtab a11y audit --axe --tags wcag2a filters"

pt_ok a11y audit --axe --tags wcag2a --json
assert_output_contains '"image-alt"' "wcag2a keeps image-alt"
if grep -q '"color-contrast"' <<<"$PT_OUT"; then
  fail_assert "color-contrast (wcag2aa) should be dropped under --tags wcag2a"
else
  pass_assert "color-contrast absent under --tags wcag2a"
fi

end_test

# ─────────────────────────────────────────────────────────────────
start_test "pinchtab a11y audit (native) summary"

pt_ok a11y audit
assert_output_contains "engine=native" "native engine summary line"

end_test
