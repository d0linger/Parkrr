package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// Any audit spelling other than auditDeleted, recording action "delete". The
// argument prefix before `r` varies (ctx/tx, r.Context()/tx, none), and the earlier
// version of this pattern pinned it too tightly: it matched only `h.audit(r,` and
// `h.audit…(ctx, tx, r,`, so a live `h.auditChange(r, "delete", …)` in
// wall_templates.go slipped straight past the guard that exists to catch it.
// Matching on the helper name plus the "delete" literal keeps it spelling-agnostic.
var reBareDelete = regexp.MustCompile(`h\.(?:audit|auditTx|auditAs|auditChange|auditChangeTx|auditInsert)\([^)]*?"delete"`)

// TestAuditDeletesCarryASnapshot fails if any deletion is recorded with a plain
// summary instead of auditDeleted, which preserves the removed row's values.
func TestAuditDeletesCarryASnapshot(t *testing.T) {
	var bad []string
	for _, hf := range handlerFuncs(t) {
		// auditDeleted is the sanctioned wrapper; its own body naturally contains the
		// delete-audit call this test forbids everywhere else.
		if hf.Name == "auditDeleted" {
			continue
		}
		if reBareDelete.MatchString(hf.Body) {
			bad = append(bad, hf.File+" "+hf.Name)
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
			// Package-level funcs count too, not just methods on *Handler: the *Tx helpers
			// (createVehiclesTx, settleItemTx, recordPeriodPaymentTx …) carry real UPDATEs
			// and DELETEs, and skipping them left those paths outside both guards.
			if !ok || fn.Body == nil {
				continue
			}
			s := fset.Position(fn.Body.Pos()).Offset
			e := fset.Position(fn.Body.End()).Offset
			out = append(out, handlerFunc{File: p, Name: fn.Name.Name, Body: string(src[s:e])})
		}
	}
	if len(out) == 0 {
		t.Fatal("no handler functions found")
	}
	return out
}

var (
	reUpdateSet = regexp.MustCompile(`(?s)UPDATE \w+ SET (.*?)(?:WHERE|RETURNING)`)
	reQuotedKey = regexp.MustCompile(`"([a-z_]+)":`)
	// A struct-based diff: diffFields(old|prev|existing, …) — or one built from a
	// local audit-view struct literal, e.g. diffFields(hallAudit{…}, hallAudit{…}).
	reStructDif = regexp.MustCompile(`diffFields\(\s*(?:old|prev|existing)\b|diffFields\(\s*\n?\s*\w+\{`)
	// Column names may contain digits (s3_cron, s3_keep) but never start with one. The
	// old ^[a-z_]+$ silently SKIPPED such columns, so a diff could omit them and pass.
	reColumn = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
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
	// `paid` on a vehicle/charge when a PAYMENT is reversed: clearing it is the
	// mechanical cascade of un-booking that payment, not an edit of those records —
	// the payment's own entry is the trail. Where `paid` IS the user's change (the
	// agreement flag) the handler audits it explicitly, so this never hides that.
	"paid":        true,
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
			// EVERY UPDATE in the body, not just the first: a handler that writes two
			// tables had only its first statement checked, so the second one's columns
			// were unguarded (saveAgreement writes vehicles AND flat_rate_periods).
			sets := reUpdateSet.FindAllStringSubmatch(body, -1)
			if len(sets) == 0 {
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
			seenCol := map[string]bool{}
			for _, set := range sets {
				for _, part := range strings.Split(strings.ReplaceAll(set[1], "\n", " "), ",") {
					col := strings.TrimSpace(strings.SplitN(part, "=", 2)[0])
					if !reColumn.MatchString(col) || auditIgnoredColumns[col] || audited[col] || seenCol[col] {
						continue
					}
					seenCol[col] = true
					missing = append(missing, col)
				}
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

// reSecretWord matches a credential-ish word. It is applied to two different
// things below (an interpolated expression, and a string literal), which is why it
// must not be run over raw source text: `u.Username+" regenerated recovery codes"`
// contains the word "recovery" in PROSE, and flagging that is a false positive.
// The AST walk separates the literal parts from the interpolated ones first.
var reSecretWord = regexp.MustCompile(`(?i)(password|passwort|secret|token|apikey|api_key|privatekey|private_key|accesskey|access_key|credential|hash|salt|totp|backup_?code|recovery)`)

// auditCallNames is every spelling that writes an audit row — including the
// package-level auditSystem in cmd/parkrr, which audits the boot-time bootstrap
// without a request.
var auditCallNames = map[string]bool{
	"audit": true, "auditTx": true, "auditAs": true, "auditInsert": true,
	"auditChange": true, "auditChangeTx": true,
	"auditCreated": true, "auditDeleted": true, "auditSystem": true,
}

// auditSummaryDirs are the trees this guard walks. cmd/parkrr is included on
// purpose: audit_redact.go names its bootstrap as the worked example of the
// convention, so the guard has to actually reach it.
var auditSummaryDirs = []string{".", filepath.Join("..", "..", "cmd", "parkrr")}

// TestAuditSummariesCarryNoSecretIdentifiers guards the one part of an audit row
// that the redaction choke point cannot reach.
//
// redactChanges filters the `changes` column by field NAME, but `summary` is free
// text concatenated at the call site — there is no field name to key a policy on.
// A handler writing `h.audit(r, "update", "user", id, "reset token to "+tok)` would
// put a credential in clear text into an append-only, seven-year table and no
// redaction test would notice. Two shapes are therefore rejected:
//
//  1. an INTERPOLATED expression whose text reads like a credential
//     (`+newPassword`, `+cfg.APIKey`) — the value itself is going into the row;
//  2. a string literal that ANNOUNCES a credential and has something concatenated
//     AFTER it (`"neues Passwort: "+x`) — the label promises the value follows,
//     whatever the variable happens to be called.
//
// A literal that merely mentions a credential in prose and ENDS the expression
// ("… regenerated recovery codes") is fine: it names the operation, not a value.
// That distinction is the whole reason this runs on the AST and not on source text.
func TestAuditSummariesCarryNoSecretIdentifiers(t *testing.T) {
	var bad []string
	for _, dir := range auditSummaryDirs {
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		for _, p := range paths {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			src, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Fatalf("read %s: %v", p, rerr)
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, p, src, 0)
			if perr != nil {
				t.Fatalf("parse %s: %v", p, perr)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isAuditCall(call.Fun) {
					return true
				}
				for _, arg := range call.Args {
					// Only a summary is built with `+`; ids, actions and changes never are.
					be, isAdd := arg.(*ast.BinaryExpr)
					if !isAdd || be.Op != token.ADD {
						continue
					}
					parts := flattenAdd(be)
					for i, part := range parts {
						txt := exprText(fset, src, part)
						where := p + ":" + strconv.Itoa(fset.Position(part.Pos()).Line)
						if lit, isLit := part.(*ast.BasicLit); isLit && lit.Kind == token.STRING {
							if i < len(parts)-1 && reSecretWord.MatchString(txt) {
								bad = append(bad, where+": label "+txt+" precedes a concatenated value")
							}
							continue
						}
						if reSecretWord.MatchString(txt) {
							bad = append(bad, where+": interpolates "+txt)
						}
					}
				}
				return true
			})
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("audit summary carries a credential — summaries are NOT redacted (only `changes` is,\n"+
			"see audit_redact.go). Log the object and the operation, never the value:\n  %s",
			strings.Join(bad, "\n  "))
	}
}

// isAuditCall reports whether a call expression is one of the audit writers,
// whether spelled as a method (h.audit) or a plain function (auditSystem).
func isAuditCall(fun ast.Expr) bool {
	switch t := fun.(type) {
	case *ast.SelectorExpr:
		return auditCallNames[t.Sel.Name]
	case *ast.Ident:
		return auditCallNames[t.Name]
	}
	return false
}

// flattenAdd turns a left-leaning `a + b + c` tree into its operands.
func flattenAdd(e ast.Expr) []ast.Expr {
	be, ok := e.(*ast.BinaryExpr)
	if !ok || be.Op != token.ADD {
		return []ast.Expr{e}
	}
	return append(flattenAdd(be.X), flattenAdd(be.Y)...)
}

// exprText returns the exact source of an expression.
func exprText(fset *token.FileSet, src []byte, e ast.Expr) string {
	return string(src[fset.Position(e.Pos()).Offset:fset.Position(e.End()).Offset])
}
