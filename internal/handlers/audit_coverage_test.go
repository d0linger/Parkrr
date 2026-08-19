package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Audit-coverage guard.
//
// The audit trail is only worth something if it stays complete as handlers are
// added and edited. These tests parse the handler sources and fail the build when
// a mutation would silently stop being provable:
//
//  1. no DELETE may audit with a bare summary — the row is gone afterwards, so an
//     id-only entry can never be resolved back to the object (use auditDeleted);
//  2. an audit call that carries field changes must cover every column its UPDATE
//     writes — a partial diff hides real edits (this is how the vehicle rename and
//     the billing-settings gaps were found).
//
// Both are deliberately source-level checks: there is no runtime hook that could
// notice a forgotten field, and a reviewer cannot hold 95 call sites in their head.

// handlerSources returns the non-test handler .go files with their contents.
func handlerSources(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	out := map[string]string{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("read %s: %v", p, rerr)
		}
		out[p] = string(b)
	}
	if len(out) == 0 {
		t.Fatal("no handler sources found")
	}
	return out
}

var reBareDelete = regexp.MustCompile(`h\.audit(?:Tx)?\((?:ctx, tx, )?r, "delete"`)

// TestAuditDeletesCarryASnapshot fails if any deletion is recorded with a plain
// summary instead of auditDeleted, which preserves the removed row's values.
func TestAuditDeletesCarryASnapshot(t *testing.T) {
	var bad []string
	for path, src := range handlerSources(t) {
		for _, loc := range reBareDelete.FindAllStringIndex(src, -1) {
			line := strings.Count(src[:loc[0]], "\n") + 1
			bad = append(bad, path+":"+itoaLine(line))
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("deletion(s) audited without a snapshot of the removed row — use h.auditDeleted so the entry\n"+
			"stays resolvable after the row is gone:\n  %s", strings.Join(bad, "\n  "))
	}
}

// handlerFunc is one method on *Handler with the exact source of its body. The
// boundaries come from go/ast, not from a regex: a regex over `func … { … }`
// silently mis-slices real functions — it dropped UpdateVehicle entirely, and a
// guard that skips the very handler it is meant to watch is worse than no guard.
type handlerFunc struct {
	File string
	Name string
	Body string
}

func handlerFuncs(t *testing.T) []handlerFunc {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []handlerFunc
	fset := token.NewFileSet()
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("read %s: %v", p, rerr)
		}
		f, perr := parser.ParseFile(fset, p, src, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", p, perr)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			s := fset.Position(fn.Body.Pos()).Offset
			e := fset.Position(fn.Body.End()).Offset
			out = append(out, handlerFunc{File: p, Name: fn.Name.Name, Body: string(src[s:e])})
		}
	}
	if len(out) == 0 {
		t.Fatal("no handler methods found")
	}
	return out
}

var (
	reUpdateSet = regexp.MustCompile(`(?s)UPDATE \w+ SET (.*?)(?:WHERE|RETURNING)`)
	reQuotedKey = regexp.MustCompile(`"([a-z_]+)":`)
	// A struct-based diff: diffFields(old|prev|existing, …) — or one built from a
	// local audit-view struct literal, e.g. diffFields(hallAudit{…}, hallAudit{…}).
	reStructDif = regexp.MustCompile(`diffFields\(\s*(?:old|prev|existing)\b|diffFields\(\s*\n?\s*\w+\{`)
	reColumn    = regexp.MustCompile(`^[a-z_]+$`)
)

// auditIgnoredColumns are columns a diff may legitimately omit.
//
//   - bookkeeping/side-effect counters that no user edits directly,
//   - settlement state that is not an edit of the record,
//   - who/when, which the audit row itself already carries,
//   - and secrets/binary payloads that must never be read into the trail.
var auditIgnoredColumns = map[string]bool{
	"updated_at": true, "created_at": true,
	"next_invoice_no": true, "paid_amount": true,
	"paid_periods": true, "paid_fixed": true, "rates_synced": true,
	"reversed_at": true, "reversed_by": true,
	"password_hash": true, "totp_secret": true,
	"data": true, "byte_size": true, "content_type": true,
	"spot_id":  true, // recorded as vehicle_id / vehicle_ids by the spot handlers
	"geometry": true, // recorded as a size state, never the blob
}

// TestAuditDiffsCoverEveryWrittenColumn fails when a handler records field changes
// but leaves out a column its own UPDATE writes.
func TestAuditDiffsCoverEveryWrittenColumn(t *testing.T) {
	var bad []string
	for _, hf := range handlerFuncs(t) {
		{
			path, name, body := hf.File, hf.Name, hf.Body
			idx := strings.Index(body, "auditChange")
			if idx < 0 {
				continue // no field-level audit here; other tests cover presence
			}
			set := reUpdateSet.FindStringSubmatch(body)
			if set == nil {
				continue
			}
			seg := body[idx:]
			// A struct-based diff — diffFields(old, …) / (prev, …) / (existing, …) —
			// compares every json-tagged field of the request or model type, so it
			// covers the written columns by construction and cannot be judged from
			// the quoted keys in the source. Only map-literal diffs, where the author
			// enumerates the fields by hand, are checkable here — and those are
			// exactly the ones that drifted. Search the WHOLE body: several handlers
			// build `changes := diffFields(old, new)` above the auditChange call.
			if reStructDif.MatchString(body) {
				continue
			}
			audited := map[string]bool{}
			for _, k := range reQuotedKey.FindAllStringSubmatch(seg, -1) {
				audited[k[1]] = true
			}
			var missing []string
			for _, part := range strings.Split(strings.ReplaceAll(set[1], "\n", " "), ",") {
				col := strings.TrimSpace(strings.SplitN(part, "=", 2)[0])
				if !reColumn.MatchString(col) || auditIgnoredColumns[col] || audited[col] {
					continue
				}
				missing = append(missing, col)
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				bad = append(bad, path+" "+name+": "+strings.Join(missing, ", "))
			}
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("audit diff(s) omit columns the UPDATE writes — a change to these fields would not be\n"+
			"provable. Add them to the diff, or to auditIgnoredColumns with a reason:\n  %s",
			strings.Join(bad, "\n  "))
	}
}

func itoaLine(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
