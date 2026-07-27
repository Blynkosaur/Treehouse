package check

import (
	"strings"
	"testing"
	"time"
)

// noon is the clock every case below is written against — Explain takes `now`
// so the sentences it produces are fixed strings, not whatever time the suite
// happened to run at.
var noon = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

func green(hour, min int, branch string) Entry {
	return Entry{Status: "ok", Green: time.Date(2026, 7, 26, hour, min, 0, 0, time.UTC), Branch: branch}
}

func TestExplain(t *testing.T) {
	tests := []struct {
		name    string
		journal Journal
		current []Check
		branch  string
		answer  string
		changes int
	}{
		{
			name:    "no journal at all",
			journal: Journal{},
			current: []Check{{Name: "db", Status: "fail", Detail: "broken"}},
			answer:  "no baseline yet — run `th doctor` first",
		},
		{
			// A journal that read back empty for any reason — corrupt, older
			// schema, truncated — arrives here as the zero Journal and must
			// answer exactly like a missing one.
			name:    "journal with a schema but no entries",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{}},
			current: []Check{{Name: "db", Status: "fail", Detail: "broken"}},
			answer:  "no baseline yet — run `th doctor` first",
		},
		{
			name:    "nothing changed",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{"env": green(9, 15, "main"), "db": green(9, 15, "main")}},
			current: []Check{{Name: "env", Status: "ok"}, {Name: "db", Status: "ok"}},
			branch:  "main",
			answer:  "nothing to explain — every check is green",
		},
		{
			name:    "one thing changed",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{"env": green(9, 15, "main"), "db": green(14, 2, "main")}},
			current: []Check{{Name: "env", Status: "ok"}, {Name: "db", Status: "fail", Detail: ".env targets the SHARED database"}},
			branch:  "main",
			answer:  "db went from ok to fail since 14:02: .env targets the SHARED database",
			changes: 1,
		},
		{
			name:    "one thing changed, on another branch",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{"db": green(14, 2, "main")}},
			current: []Check{{Name: "db", Status: "fail", Detail: "no clone"}},
			branch:  "feat/login",
			answer:  "db went from ok to fail after you switched to feat/login: no clone",
			changes: 1,
		},
		{
			name:    "several things changed",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{"env": green(9, 15, "main"), "db": green(9, 15, "main")}},
			current: []Check{
				{Name: "env", Status: "warn", Detail: "REDIS_URL missing"},
				{Name: "db", Status: "fail", Detail: "no clone"},
			},
			branch:  "main",
			answer:  "2 things changed since everything was green:",
			changes: 2,
		},
		{
			// The governing rule: a check that stopped running is not "still
			// fine", and it does not get a failure's phrasing either.
			name:    "ok to skip says the check stopped running",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{"db": green(14, 2, "main")}},
			current: []Check{{Name: "db", Status: skip, Detail: "postgres is not reachable"}},
			branch:  "main",
			answer:  "db stopped being checked since 14:02: postgres is not reachable",
			changes: 1,
		},
		{
			name:    "a check that disappeared entirely",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{"env": green(9, 15, "main"), "db": green(14, 2, "main")}},
			current: []Check{{Name: "env", Status: "ok"}},
			branch:  "main",
			answer:  "db is not being reported at all any more (last ok 14:02)",
			changes: 1,
		},
		{
			// Still broken, same status as the last run: "went from ok to fail"
			// would date the break to this run, which is a lie by two days.
			name:    "unchanged since the last run reads as still",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{"db": {Status: "fail", Green: time.Date(2026, 7, 24, 14, 2, 0, 0, time.UTC), Branch: "main"}}},
			current: []Check{{Name: "db", Status: "fail", Detail: "no clone"}},
			branch:  "main",
			answer:  "db has been fail since Jul 24 14:02: no clone",
			changes: 1,
		},
		{
			name:    "a row the journal has never seen",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{"env": green(9, 15, "main")}},
			current: []Check{{Name: "env", Status: "ok"}, {Name: "env (api)", Status: "warn", Detail: "KEY missing"}},
			branch:  "main",
			answer:  "env (api) is new since the last run: KEY missing",
			changes: 1,
		},
		{
			// Known but never green: there is no "since" to report, and inventing
			// one from the zero time would date the break to year 1.
			name:    "a row that has never been green",
			journal: Journal{Schema: JournalSchema, Entries: map[string]Entry{"config": {Status: "fail"}}},
			current: []Check{{Name: "config", Status: "fail", Detail: "could not parse treehouse.toml"}},
			branch:  "main",
			answer:  "config has never been green: could not parse treehouse.toml",
			changes: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := Explain(tt.journal, tt.current, tt.branch, noon)
			if w.Answer != tt.answer {
				t.Errorf("answer:\n got %q\nwant %q", w.Answer, tt.answer)
			}
			if len(w.Changes) != tt.changes {
				t.Errorf("changes: got %d %v, want %d", len(w.Changes), w.Changes, tt.changes)
			}
			if want := len(tt.journal.Entries) > 0; w.Baseline != want {
				t.Errorf("baseline: got %v, want %v", w.Baseline, want)
			}
		})
	}
}

// A single change IS the answer — the one-line promise. More than one and the
// answer is a headline over the list, so nothing is dropped.
func TestExplainAnswerIsTheOneLine(t *testing.T) {
	j := Journal{Schema: JournalSchema, Entries: map[string]Entry{"db": green(14, 2, "main")}}
	w := Explain(j, []Check{{Name: "db", Status: "fail", Detail: "no clone"}}, "main", noon)
	if len(w.Changes) != 1 || w.Answer != w.Changes[0] {
		t.Errorf("one change should be the answer verbatim: %q vs %v", w.Answer, w.Changes)
	}
}

func TestRecord(t *testing.T) {
	nine := time.Date(2026, 7, 26, 9, 15, 0, 0, time.UTC)

	j := Journal{}.Record([]Check{
		{Name: "env", Status: "ok"},
		{Name: "db", Status: "fail"},
	}, "main", nine)
	if j.Schema != JournalSchema {
		t.Errorf("schema %d, want %d", j.Schema, JournalSchema)
	}
	if got := j.Entries["env"]; !got.Green.Equal(nine) || got.Branch != "main" {
		t.Errorf("ok row should record its green: %+v", got)
	}
	if got := j.Entries["db"]; !got.Green.IsZero() || got.Status != "fail" {
		t.Errorf("a failing row has no green to record: %+v", got)
	}

	// A later run that breaks env must keep env's last-green, and must not
	// forget a row it no longer reports.
	j = j.Record([]Check{{Name: "env", Status: "warn"}}, "feat/login", noon)
	if got := j.Entries["env"]; !got.Green.Equal(nine) || got.Status != "warn" || got.Branch != "main" {
		t.Errorf("last-green must survive a later break: %+v", got)
	}
	if _, ok := j.Entries["db"]; !ok {
		t.Error("a row this run did not produce must keep its history")
	}
}

func TestSnapshot(t *testing.T) {
	root := "/repo"
	tests := []struct {
		name    string
		finding Finding
		want    Check
	}{
		{
			name:    "clean root service",
			finding: Finding{Dir: "/repo", Keys: 3},
			want:    Check{Name: "env", Status: "ok", Detail: "all 3 expected keys present"},
		},
		{
			name:    "drift warns and names the keys",
			finding: Finding{Dir: "/repo", Keys: 3, Missing: []string{"REDIS_URL"}, Empty: []string{"API_KEY"}},
			want:    Check{Name: "env", Status: "warn", Detail: "REDIS_URL missing, API_KEY empty", Fix: "th hydrate"},
		},
		{
			name:    "a curated key fails",
			finding: Finding{Dir: "/repo", Keys: 3, Missing: []string{"REDIS_URL"}, Failed: []string{"REDIS_URL"}},
			want:    Check{Name: "env", Status: "fail", Detail: "REDIS_URL missing (required by treehouse.toml)", Fix: "th hydrate"},
		},
		{
			name:    "a service names itself",
			finding: Finding{Dir: "/repo/api", Keys: 1, NoEnv: true, Missing: []string{"KEY"}},
			want:    Check{Name: "env (api)", Status: "warn", Detail: ".env missing entirely (1 keys expected)", Fix: "th hydrate"},
		},
		{
			name:    "a wide drift stays one line",
			finding: Finding{Dir: "/repo", Keys: 9, Missing: []string{"A", "B", "C", "D", "E"}},
			want:    Check{Name: "env", Status: "warn", Detail: "A, B, C and 2 more missing", Fix: "th hydrate"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Snapshot(root, []Finding{tt.finding}, nil)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}

	t.Run("checks come through untouched, after the env rows", func(t *testing.T) {
		db := Check{Name: "db", Status: "ok", Detail: "own clone"}
		got := Snapshot(root, []Finding{{Dir: "/repo", Keys: 1}}, []Check{db})
		if len(got) != 2 || got[1] != db {
			t.Errorf("got %+v", got)
		}
	})
}

// The whole loop, from a green run to the sentence a human reads: doctor
// records, something breaks, why explains it.
func TestJournalRoundTrip(t *testing.T) {
	nine := time.Date(2026, 7, 26, 9, 15, 0, 0, time.UTC)
	j := Journal{}.Record(Snapshot("/repo", []Finding{{Dir: "/repo", Keys: 2}}, []Check{{Name: "db", Status: "ok"}}), "main", nine)

	broken := Snapshot("/repo", []Finding{{Dir: "/repo", Keys: 2, Missing: []string{"REDIS_URL"}}}, []Check{{Name: "db", Status: "ok"}})
	w := Explain(j, broken, "main", noon)
	if want := "env went from ok to warn since 09:15: REDIS_URL missing"; w.Answer != want {
		t.Errorf("\n got %q\nwant %q", w.Answer, want)
	}
	if !strings.Contains(w.Answer, "REDIS_URL") {
		t.Error("the one line has to name the key that moved")
	}
}
