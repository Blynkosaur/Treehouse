package check

import (
	"strings"
	"testing"
)

// errSecrets reports a secrets verdict that came out wrong.
type errSecrets struct{ scenario, want, got string }

func (e errSecrets) Error() string {
	return e.scenario + ": want " + e.want + ", got " + e.got
}

// TestCheckSecrets walks the four tiers. The last one is the rule that runs
// through this whole report: nothing to check is not the same sentence as
// checked and fine.
func TestCheckSecrets(t *testing.T) {
	for _, c := range []struct {
		name     string
		curated  []string
		vars     map[string]string
		dangling []string
		want     []string // "name=status" per row, in order
		mentions string
	}{{
		name: "a secret-looking key in cleartext is inferred, so it warns",
		vars: map[string]string{"PORT": "3000", "STRIPE_SECRET": "sk-live-x"},
		want: []string{"secrets=warn"}, mentions: "STRIPE_SECRET",
	}, {
		name:    "the same key named in treehouse.toml is curated, so it fails",
		curated: []string{"STRIPE_SECRET"},
		vars:    map[string]string{"STRIPE_SECRET": "sk-live-x"},
		want:    []string{"secrets=fail"}, mentions: "STRIPE_SECRET",
	}, {
		name:    "a curated key with an unremarkable name still fails",
		curated: []string{"DEPLOY_HOOK"},
		vars:    map[string]string{"DEPLOY_HOOK": "https://hooks.example/abc"},
		want:    []string{"secrets=fail"}, mentions: "DEPLOY_HOOK",
	}, {
		name:     "a reference to a secret that is not there fails, and names the fix",
		vars:     map[string]string{"STRIPE_SECRET": "th:STRIPE_SECRET"},
		dangling: []string{"STRIPE_SECRET"},
		want:     []string{"vault=fail"}, mentions: "th vault add STRIPE_SECRET",
	}, {
		name: "a vaulted key that resolves is not a row at all",
		vars: map[string]string{"STRIPE_SECRET": "th:STRIPE_SECRET", "PORT": "3000"},
		want: nil,
	}, {
		// An empty value cannot leak, and CheckEnv already nags about it.
		name: "an empty secret-looking key is CheckEnv's business, not ours",
		vars: map[string]string{"API_TOKEN": ""},
		want: nil,
	}, {
		name: "no secret-looking keys and no config is silence, not a green row",
		vars: map[string]string{"PORT": "3000", "COMPOSE_PROJECT_NAME": "app_feat"},
		want: nil,
	}, {
		name:     "both problems at once get a row each, dangling first",
		curated:  []string{"DB_PASSWORD"},
		vars:     map[string]string{"DB_PASSWORD": "hunter2", "API_TOKEN": "th:API_TOKEN"},
		dangling: []string{"API_TOKEN"},
		want:     []string{"vault=fail", "secrets=fail"},
	}} {
		t.Run(c.name, func(t *testing.T) {
			got := Doctor{Secrets: c.curated}.CheckSecrets(c.vars, c.dangling)
			var names []string
			var all string
			for _, row := range got {
				names = append(names, row.Name+"="+row.Status)
				all += row.Detail + " " + row.Fix + "\n"
			}
			if strings.Join(names, ",") != strings.Join(c.want, ",") {
				t.Fatal(errSecrets{c.name, strings.Join(c.want, ","), strings.Join(names, ",")})
			}
			if c.mentions != "" && !strings.Contains(all, c.mentions) {
				t.Fatal(errSecrets{c.name, "a row mentioning " + c.mentions, all})
			}
			// Whatever else a row says, it must never say the secret itself.
			for _, val := range c.vars {
				if len(val) > 4 && strings.Contains(all, val) {
					t.Fatal(errSecrets{c.name, "no values in the report", all})
				}
			}
		})
	}
}

// TestLooksSecretIsNotOverEager: the inferred tier is a WARN on every repo that
// installs treehouse, so a heuristic that fires on PORT teaches people to stop
// reading the report.
func TestLooksSecret(t *testing.T) {
	for _, key := range []string{"STRIPE_SECRET", "DB_PASSWORD", "API_TOKEN", "OPENAI_API_KEY", "SIGNING_KEY", "AWS_CREDENTIALS", "jwt_secret"} {
		if !looksSecret(key) {
			t.Errorf("looksSecret(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"PORT", "ADMIN_PORT", "DATABASE_URL", "REDIS_URL", "COMPOSE_PROJECT_NAME", "NODE_ENV", "KEYCLOAK_HOST", "MONKEY"} {
		if looksSecret(key) {
			t.Errorf("looksSecret(%q) = true — a false alarm on every repo is how a report stops being read", key)
		}
	}
}
