#!/usr/bin/env bash
#
# CSP inline-style guard.
#
# The SPA is served under a strict `style-src 'self'` Content-Security-Policy, which
# blocks inline STYLE ATTRIBUTES: the browser rejects `element.style.cssText = ...`,
# `element.style = "..."`, `element.style['cssText'] = ...`, `setAttribute('style', ...)`,
# and `style="..."` attributes parsed from innerHTML/SVG markup. Set styles PER PROPERTY
# instead — `element.style.left = '4px'` — which the CSP allows. This guard fails if a
# forbidden form (including common whitespace/bracket evasions) reappears in the
# frontend JS, so the fixes in commit history cannot silently regress.
#
# A deliberate, reviewed exception may carry the marker `csp-allow-inline-style` on the
# SAME line to be skipped.
#
# Run locally:      bash scripts/csp-inline-style-check.sh
# Self-test only:   bash scripts/csp-inline-style-check.sh --self-test
set -uo pipefail

# Banned inline-style forms (POSIX ERE). Whitespace variants are tolerated so trivial
# reformatting cannot slip past the guard.
PATTERNS=(
  '\.style[[:space:]]*\.[[:space:]]*cssText[[:space:]]*='          # .style.cssText =   (incl. spaced dots)
  "\.style[[:space:]]*\[[[:space:]]*['\"]cssText['\"]"             # .style['cssText']  (bracket access)
  "\.style[[:space:]]*=[[:space:]]*['\"]"                          # el.style = "..."   (cssText-equivalent)
  "setAttribute[[:space:]]*\([[:space:]]*['\"][[:space:]]*style"   # setAttribute('style', ...) (spaced)
  'style=["'\'']'                                                  # style="..." attribute in markup
)
# The markup check stays whitespace-free (`style=`) on purpose: a lenient
# `style[[:space:]]*=` would also flag JS variable assignments named `style`
# (e.g. `const style = "..."`), a false positive. The `.style =` pattern above already
# covers the real cssText-equivalent assignment via its required leading dot.

# grep_hits <path> -> "file:line:text" for every banned form, excluding marked exceptions.
grep_hits() {
  local target="$1" pat
  for pat in "${PATTERNS[@]}"; do
    grep -rnE "$pat" "$target" 2>/dev/null
  done | grep -v 'csp-allow-inline-style' | sort -u
}

run_scan() {
  local dir="${1:-web/static/js}" hits
  hits="$(grep_hits "$dir" || true)"
  if [ -n "$hits" ]; then
    printf '%s\n' "$hits"
    echo "  -> inline style attribute / cssText is CSP-blocked; set styles per property"
    echo "     via the CSSOM (or mark a reviewed exception with csp-allow-inline-style)."
    echo "CSP inline-style guard: FAILED"
    exit 1
  fi
  echo "CSP inline-style guard: clean"
}

self_test() {
  local tmp rc=0; tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' RETURN
  # Forms that MUST be caught (regression fixtures for each supported evasion).
  cat > "$tmp/evasions.js" <<'JS'
a.style.cssText = 'x';
a.style . cssText = 'x';
a.style['cssText'] = 'x';
a.style[ "cssText" ] = 'x';
a.style = 'color:red';
a.style = "color:red";
el.setAttribute('style','x');
el.setAttribute ( "style", "x");
var s = '<div style="x">';
JS
  # Forms that must NOT be caught (safe per-property + look-alikes + a marked exception).
  cat > "$tmp/safe.js" <<'JS'
a.style.left = '1px';
a.style.animationDelay = '-1s';
const style = 'solid';
let borderStyle = "dashed";
a.setAttribute('data-style','x');
w.write('<div style="x">'); // csp-allow-inline-style: reviewed
JS
  local want got safe_hits
  want=$(grep -cvE '^[[:space:]]*$' "$tmp/evasions.js")
  got=$(grep_hits "$tmp/evasions.js" | grep -c ':' || true)
  if [ "$got" -lt "$want" ]; then
    echo "SELF-TEST FAIL: caught $got/$want evasion forms"; grep_hits "$tmp/evasions.js"; rc=1
  else
    echo "self-test: all $want evasion forms caught"
  fi
  safe_hits="$(grep_hits "$tmp/safe.js" || true)"
  if [ -n "$safe_hits" ]; then
    echo "SELF-TEST FAIL: false positive on safe forms:"; printf '%s\n' "$safe_hits"; rc=1
  else
    echo "self-test: no false positives on safe forms"
  fi
  return "$rc"
}

if [ "${1:-}" = "--self-test" ]; then
  self_test; exit $?
fi
# CI runs the self-test first (fast, no deps) so the guard's own patterns are verified,
# then scans the real tree.
self_test || exit 1
echo "---"
run_scan "${1:-web/static/js}"
