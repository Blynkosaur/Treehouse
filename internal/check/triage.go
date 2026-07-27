package check

import (
	"regexp"
	"strings"
)

// Signature is one recognisable failure shape: a regex over a command's output,
// what it means, the command that fixes it — and, the part that makes this more
// than grep, which doctor fact has to agree before we say "environment".
//
// Built-in defaults (DefaultSignatures) cover the three failures B1 names;
// treehouse.toml [[signature]] entries add or override them by name, the same
// extension point [[deps]] and [[seed]] are.
type Signature struct {
	Name  string `toml:"name"`
	Match string `toml:"match"` // regex, matched line by line against the output
	Cause string `toml:"cause"` // what this pattern means, in one line
	Fix   string `toml:"fix"`   // the command that would resolve it
	Needs string `toml:"needs"` // env | db | migration | service — which doctor fact must corroborate
}

// The areas a signature can lean on. They are the doctor facts that exist, plus
// one that doesn't — see areas().
const (
	needsEnv       = "env"
	needsDB        = "db"
	needsMigration = "migration"
	needsService   = "service"
)

// DefaultSignatures are the zero-config built-ins, one per failure in B1's AC.
// Deliberately few: a signature that fires on the wrong thing is worse than no
// signature, because the verdict it produces is confident.
func DefaultSignatures() []Signature {
	return []Signature{
		{
			Name:  "connection-refused",
			Match: `(?i)connection refused|ECONNREFUSED|could not connect to server`,
			Cause: "nothing is listening on the address the app dialled",
			Fix:   "start the service (docker compose up -d), then re-run",
			Needs: needsService,
		},
		{
			Name:  "missing-relation",
			Match: `(?i)relation "[^"]*" does not exist|UndefinedTable|no such table`,
			Cause: "the database is missing a table this code expects",
			Fix:   "run your migrations against this worktree's database, then `th seed <name>`",
			Needs: needsMigration,
		},
		{
			Name: "missing-env",
			// Python's KeyError on os.environ, Node's "must be set", and the
			// generic "environment variable X is not set" all name a SCREAMING
			// key — that shape is what keeps this off an ordinary dict KeyError.
			Match: `KeyError: ['"][A-Z][A-Z0-9_]{2,}['"]|[Ee]nvironment variable [A-Z][A-Z0-9_]{2,}|[A-Z][A-Z0-9_]{2,} (?:is )?(?:not set|must be set|is required|is undefined)`,
			Cause: "a required environment variable is unset",
			Fix:   "th hydrate",
			Needs: needsEnv,
		},
	}
}

// TriageVerdict is B1's answer: environment, code, or an honest unknown.
//
// Not called Verdict because Verdict is already the function that folds
// doctor's own findings into ok/warn/fail, and one word in one package cannot
// be both a function and a type.
type TriageVerdict struct {
	Cause     string   `json:"cause"`               // environment | code | unknown
	Signature string   `json:"signature,omitempty"` // the signature that matched; "" = none did
	Evidence  []string `json:"evidence"`            // why, in the order a reader wants it
	Fixes     []string `json:"fixes,omitempty"`     // every fix we have evidence for
}

// Triage correlates a failed command's output with doctor's own state.
//
// The correlation is the whole point, and it is what a signature map alone
// cannot do. A regex that matches proves the output LOOKS like an environment
// failure; only doctor can say whether the environment is actually broken. When
// they disagree — the pattern matched but that part of the environment is
// verified healthy — the answer is `unknown` with the contradiction spelled
// out, never `environment`. A confident wrong "it's your environment" sends an
// agent to reinstall Postgres for twenty minutes over a typo in its own code,
// which is strictly worse than saying nothing.
//
// Both halves are passed in: Triage does no I/O, so the whole table below is
// testable from struct literals.
//
//	signature | doctor for that area | verdict
//	----------+----------------------+-------------------------------------------
//	matched   | red                  | environment (evidence and fixes from both)
//	matched   | green                | unknown (the evidence names the contradiction)
//	none      | red somewhere        | unknown (the red finding offered as related)
//	none      | green                | code
//
// A signature whose Match does not compile is skipped: it comes from a
// hand-edited treehouse.toml, and a broken pattern there must not take out
// triage for every other signature.
func Triage(output string, findings []Finding, checks []Check, sigs []Signature) TriageVerdict {
	facts := areas(findings, checks)
	sig, line := MatchSignature(output, sigs)

	// Nothing matched: the only thing that can move the verdict off `code` is
	// doctor being red on its own, and that is a hint, never a cause — no line
	// of this output connects the two.
	if sig.Name == "" {
		related := relatedLines(facts, "")
		if len(related) == 0 {
			return TriageVerdict{
				Cause:    "code",
				Evidence: []string{"no known failure signature matched, and doctor reports the environment healthy"},
			}
		}
		return TriageVerdict{
			Cause:    "unknown",
			Evidence: append([]string{"no known failure signature matched"}, related...),
		}
	}

	v := TriageVerdict{Signature: sig.Name, Evidence: []string{line}}
	if sig.Cause != "" {
		v.Evidence = append(v.Evidence, sig.Cause)
	}
	fact := facts[sig.Needs]

	switch {
	case fact.known && fact.red:
		v.Cause = "environment"
		v.Evidence = append(v.Evidence, "doctor agrees: "+fact.detail)
		v.Fixes = addFix(addFix(nil, sig.Fix), fact.fix)

	case fact.known:
		// The row that earns the correlation. The output looks environmental and
		// the environment is verified fine, so the honest answer is that we do
		// not know — and the reader gets told exactly why we stopped short.
		v.Cause = "unknown"
		v.Evidence = append(v.Evidence,
			"but doctor reports "+sig.Needs+" healthy: "+fact.detail,
			"the pattern and the environment disagree — this is more likely code than environment")
		v.Fixes = addFix(nil, sig.Fix)

	default:
		// No such doctor fact — today that is only `service`. Say so instead of
		// reading silence as green.
		v.Cause = "unknown"
		v.Evidence = append(v.Evidence, "unconfirmed: "+fact.detail)
		v.Fixes = addFix(nil, sig.Fix)
	}

	// A red area the signature did not name is a hint worth carrying, whatever
	// the verdict — the matched line and the red row may well be the same story.
	v.Evidence = append(v.Evidence, relatedLines(facts, sig.Needs)...)
	for _, name := range areaOrder {
		if f := facts[name]; name != sig.Needs && f.known && f.red {
			v.Fixes = addFix(v.Fixes, f.fix)
		}
	}
	return v
}

// area is one doctor fact a signature can lean on. `known` is the load-bearing
// field: absent and skipped checks are NOT green, and treating them as green
// would turn "we never asked" into "we verified it".
type area struct {
	known  bool
	red    bool
	detail string
	fix    string
}

// areaOrder keeps the related-hint lines deterministic; map iteration isn't.
var areaOrder = []string{needsEnv, needsDB, needsMigration, needsService}

// areas folds doctor's two lists into the facts a signature can cite.
//
// `service` is the one that took two phases to become real. It used to map onto
// nothing, so `connection refused` — the signature this whole file was written
// for — could never be corroborated and always degraded to regex-only evidence
// with cause `unknown`. CheckServices fills it now: a dead listener is a red
// service fact, and the verdict reaches `environment` with the matched line and
// the dead service side by side.
func areas(findings []Finding, checks []Check) map[string]area {
	out := map[string]area{
		needsService: {detail: "doctor found no service to check — this repo declares no PORT keys and no [[service]] entries"},
		needsDB:      {detail: "doctor did not report on the database"},
		needsMigration: {
			detail: "doctor did not report on migrations (they need `th doctor --db` and a [migrations] status command)",
		},
		needsEnv: {detail: "doctor found no env files to compare"},
	}

	if len(findings) > 0 {
		a := area{known: true, detail: "every service's .env has the keys its reference declares"}
		for _, f := range findings {
			if f.Drifted() {
				a.red, a.fix = true, "th hydrate"
				a.detail = "env drift in " + f.Dir + ": " + f.Summary()
				break
			}
		}
		out[needsEnv] = a
	}

	// worst is which row already spoke for each area. Services arrive one row per
	// detected port, so the fact has to be the WORST of them rather than the last
	// one walked: `connection refused` is about the dead one, and a healthy
	// sibling must not vote it green.
	worst := map[string]string{}
	for _, c := range checks {
		name := c.Name
		if name == "migrations" {
			name = needsMigration // the check is plural, the signature field is not
		}
		if _, ok := out[name]; !ok {
			continue // seed and anything later: no signature leans on it yet
		}
		if seen, ok := worst[name]; ok && worse(seen, c.Status) == seen {
			continue
		}
		worst[name] = c.Status
		// skip is not green: it means the question does not apply here, which is
		// exactly as uninformative as never having asked.
		if c.Status == skip {
			out[name] = area{detail: c.Detail}
			continue
		}
		out[name] = area{
			known:  true,
			red:    c.Status == "fail" || c.Status == "warn",
			detail: c.Detail,
			fix:    c.Fix,
		}
	}
	return out
}

// relatedLines offers every red area except the one already spoken for. These
// are hints, and they say so — nothing in the output tied them to this failure.
func relatedLines(facts map[string]area, except string) []string {
	var out []string
	for _, name := range areaOrder {
		if f := facts[name]; name != except && f.known && f.red {
			out = append(out, "possibly related — doctor: "+f.detail)
		}
	}
	return out
}

// MatchSignature returns the first signature that matches, and the line it
// matched. Order is the tiebreak: config-supplied signatures append, so a
// built-in wins unless it was overridden by name.
//
// Exported for the hook, which uses it as a cheap pre-filter: no match means
// nothing to say, and it can go silent without asking Postgres or git anything.
// That matters because the hook runs after EVERY Bash tool call.
func MatchSignature(output string, sigs []Signature) (Signature, string) {
	lines := strings.Split(output, "\n")
	for _, sig := range sigs {
		re, err := regexp.Compile(sig.Match)
		if err != nil || sig.Match == "" {
			continue
		}
		for _, line := range lines {
			if re.MatchString(line) {
				return sig, trim(line)
			}
		}
	}
	return Signature{}, ""
}

// trim makes one output line quotable: no surrounding space, no runaway width.
// A minified stack trace on one 40 KB line would otherwise be the whole verdict.
func trim(line string) string {
	line = strings.TrimSpace(line)
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	return line
}

// Summary is one drifted finding in a phrase, for the places that quote a
// finding inside a sentence rather than printing it as a report — triage's
// evidence and the SessionStart context. Empty when nothing drifted.
func (f Finding) Summary() string {
	switch {
	case f.NoEnv:
		return ".env missing entirely"
	case len(f.Missing) > 0:
		return "missing " + strings.Join(f.Missing, ", ")
	case len(f.Empty) > 0:
		return "empty " + strings.Join(f.Empty, ", ")
	default:
		return ""
	}
}

// addFix appends a fix unless it is empty or already there — two red areas that
// both say "th hydrate" should say it once.
func addFix(fixes []string, fix string) []string {
	if fix == "" {
		return fixes
	}
	for _, f := range fixes {
		if f == fix {
			return fixes
		}
	}
	return append(fixes, fix)
}
