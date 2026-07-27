package check

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/Blynkosaur/treehouse/internal/envfile"
)

// TestInferServices: the PORT keys E3 shifts are the service list. Nothing
// parses a compose file, and nothing invents a service a .env does not declare.
func TestInferServices(t *testing.T) {
	wt := Worktree{Root: "/w", EnvFiles: []envfile.File{
		{Path: "/w/.env", Vars: map[string]string{"PORT": "3000", "DATABASE_URL": "postgres://h/db"}},
		{Path: "/w/api/.env", Vars: map[string]string{"PORT": "4000", "ADMIN_PORT": "4001"}},
		// Neither is a port: one is out of range, one is not a port key at all.
		{Path: "/w/junk/.env", Vars: map[string]string{"PORT": "80", "TIMEOUT": "9000"}},
		// A reference file is not a declaration of anything running.
		{Path: "/w/other/.env.example", Vars: map[string]string{"PORT": "7000"}},
	}}

	want := []Service{
		{Name: "PORT", Addr: "127.0.0.1:3000"},
		{Name: "api/ADMIN_PORT", Addr: "127.0.0.1:4001"},
		{Name: "api/PORT", Addr: "127.0.0.1:4000"},
	}
	got := InferServices(wt)
	if len(got) != len(want) {
		t.Fatalf("got %d services %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("service %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	if s := InferServices(Worktree{Root: "/w"}); len(s) != 0 {
		t.Errorf("a repo declaring no ports has no services, got %v", s)
	}
}

// TestCheckServices is the tier rule: an inferred port is a guess and warns, a
// [[service]] entry is a human's word and fails. Nothing here dials anything.
func TestCheckServices(t *testing.T) {
	cases := []struct {
		name       string
		services   []Service
		up         map[string]bool
		want       string
		wantDetail string
		wantFix    string
	}{
		{
			name:       "an inferred port with a listener",
			services:   []Service{{Name: "api/PORT", Addr: "127.0.0.1:4000"}},
			up:         map[string]bool{"127.0.0.1:4000": true},
			want:       "ok",
			wantDetail: "api/PORT is listening",
		},
		{
			// Inferred requirements get softer teeth — the same bargain an
			// inferred env key gets against a curated `required` one.
			name:       "an inferred port with nothing behind it warns",
			services:   []Service{{Name: "api/PORT", Addr: "127.0.0.1:4000"}},
			want:       "warn",
			wantDetail: "nothing is listening on 127.0.0.1:4000",
			wantFix:    "docker compose up -d",
		},
		{
			name:       "a declared service with nothing behind it fails",
			services:   []Service{{Name: "api", Addr: "127.0.0.1:4000", Fix: "docker compose up -d api", Declared: true}},
			want:       "fail",
			wantDetail: "nothing is listening",
			wantFix:    "docker compose up -d api",
		},
		{
			name:     "a declared service that is up is still just ok",
			services: []Service{{Name: "api", Addr: "127.0.0.1:4000", Declared: true}},
			up:       map[string]bool{"127.0.0.1:4000": true},
			want:     "ok",
		},
		{
			// A typo in treehouse.toml is not a dead service, and it is not a
			// healthy one either — nobody could ask.
			name:       "a declared service with no addr is skip, never ok and never fail",
			services:   []Service{{Name: "api", Declared: true}},
			want:       skip,
			wantDetail: "declares no addr",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (Doctor{}).CheckServices(c.services, c.up)
			if len(got) != 1 {
				t.Fatalf("got %d rows, want 1", len(got))
			}
			if got[0].Name != "service" {
				t.Errorf("name = %q, want service — triage keys off it", got[0].Name)
			}
			if got[0].Status != c.want {
				t.Errorf("status = %q, want %q (%s)", got[0].Status, c.want, got[0].Detail)
			}
			if !strings.Contains(got[0].Detail, c.wantDetail) {
				t.Errorf("detail = %q, want it to mention %q", got[0].Detail, c.wantDetail)
			}
			if c.wantFix != "" && got[0].Fix != c.wantFix {
				t.Errorf("fix = %q, want %q", got[0].Fix, c.wantFix)
			}
			if got[0].Status != "ok" && got[0].Fix == "" {
				t.Error("a problem with no fix line is a problem the reader has to solve twice")
			}
		})
	}

	// The rule that separates "nothing to check" from "checked and fine".
	if got := (Doctor{}).CheckServices(nil, nil); len(got) != 0 {
		t.Errorf("no services must produce no rows, got %v", got)
	}
}

// TestDialServices is the one impure half, against a real listener and a port
// nobody holds — the only way to know the dial actually distinguishes them.
func TestDialServices(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener available:", err)
	}
	defer ln.Close()
	live := ln.Addr().String()

	// A port that WAS bound and then released: as close to "nothing is there"
	// as a test can get without guessing a number somebody else owns.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no second loopback listener available:", err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	up := DialServices([]Service{
		{Name: "live", Addr: live},
		{Name: "dead", Addr: deadAddr},
		{Name: "unconfigured"}, // no addr: never dialled, never reported up
		{Name: "garbage", Addr: "not-an-address"},
	})
	if !up[live] {
		t.Errorf("a live listener at %s read as down", live)
	}
	if up[deadAddr] {
		t.Errorf("a closed port at %s read as up", deadAddr)
	}
	if up[""] || up["not-an-address"] {
		t.Error("an address that cannot be dialled must never read as up")
	}
}

// TestCheckBase: a stale branch is a smell, not a broken environment.
func TestCheckBase(t *testing.T) {
	cases := []struct {
		name       string
		behind     int
		want       string
		wantDetail string
	}{
		{"current with main", 0, "ok", "up to date with main"},
		{"one behind", 1, "warn", "1 commit(s) behind main"},
		{"a long way behind is still only a warning", 400, "warn", "400 commit(s) behind main"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (Doctor{}).CheckBase(c.behind, "main")
			if got.Name != "base" {
				t.Errorf("name = %q", got.Name)
			}
			if got.Status != c.want {
				t.Errorf("status = %q, want %q", got.Status, c.want)
			}
			if got.Detail != c.wantDetail {
				t.Errorf("detail = %q, want %q", got.Detail, c.wantDetail)
			}
			if c.want == "warn" && got.Fix != "git fetch && git rebase origin/main" {
				t.Errorf("fix = %q", got.Fix)
			}
		})
	}
}

// TestServicesNeverFail pins the tier against Verdict, because that is the
// field the exit code reads: an inferred dead port must not start exiting 2 on
// people who never asked treehouse to care about it.
func TestServicesNeverFail(t *testing.T) {
	inferred := (Doctor{}).CheckServices([]Service{{Name: "api/PORT", Addr: "127.0.0.1:4000"}}, nil)
	if got := Verdict(nil, inferred); got != "warn" {
		t.Errorf("Verdict over an inferred dead service = %q, want warn", got)
	}
	declared := (Doctor{}).CheckServices([]Service{{Name: "api", Addr: "127.0.0.1:4000", Declared: true}}, nil)
	if got := Verdict(nil, declared); got != "fail" {
		t.Errorf("Verdict over a declared dead service = %q, want fail", got)
	}
}

// A live listener plus InferServices, end to end: the port a .env declares is
// the port that gets dialled, and the row comes back green.
func TestServicesEndToEnd(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener available:", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	wt := Worktree{Root: "/w", EnvFiles: []envfile.File{
		{Path: "/w/api/.env", Vars: map[string]string{"PORT": fmt.Sprint(port)}},
	}}
	services := InferServices(wt)
	checks := (Doctor{}).CheckServices(services, DialServices(services))
	if len(checks) != 1 || checks[0].Status != "ok" {
		t.Fatalf("checks = %+v, want one ok row for the live listener", checks)
	}
}
