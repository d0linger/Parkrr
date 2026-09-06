#!/usr/bin/env bash
#
# CSP inline-style guard.
#
# The SPA is served under a strict `style-src 'self'` Content-Security-Policy, which
# blocks inline STYLE ATTRIBUTES: the browser rejects `element.style.cssText = ...`,
# `setAttribute('style', ...)`, and `style="..."` attributes parsed from innerHTML/SVG
# markup. Set styles PER PROPERTY instead — `element.style.left = '4px'` — which the
# CSP allows. This guard fails if a forbidden form reappears in the frontend JS, so the
# fixes in commit history cannot silently regress.
#
# A deliberate, reviewed exception (e.g. a separate print-popup document that is not the
# app's CSP context) may carry the marker `csp-allow-inline-style` on the SAME line to
# be skipped.
#
# Run locally:  bash scripts/csp-inline-style-check.sh
set -uo pipefail

DIR="web/static/js"
fail=0

check() {
  local pattern="$1" hint="$2" hits
  hits="$(grep -rnE "$pattern" "$DIR" 2>/dev/null | grep -v 'csp-allow-inline-style' || true)"
  if [ -n "$hits" ]; then
    printf '%s\n' "$hits"
    printf '  -> %s\n\n' "$hint"
    fail=1
  fi
}

check '\.style\.cssText[[:space:]]*=' \
  "cssText is CSP-blocked; assign per property instead, e.g. node.style.x = '...'"
check "setAttribute\([[:space:]]*['\"]style" \
  "setAttribute('style', ...) is CSP-blocked; set node.style.x instead"
check 'style=["'\'']' \
  "inline style attribute in markup is CSP-blocked; set it via the CSSOM after insertion (or mark a reviewed exception with csp-allow-inline-style)"

if [ "$fail" -ne 0 ]; then
  echo "CSP inline-style guard: FAILED (see above)"
  exit 1
fi
echo "CSP inline-style guard: clean"
