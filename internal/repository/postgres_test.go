package repository

import "testing"

// A DSN reaches the platform log whenever a shard fails to open. These cases
// pin the property that the password never travels with it.
func TestRedactDSNHidesPassword(t *testing.T) {
	const secret = "SuperSecretPassword123"

	cases := []struct {
		name string
		dsn  string
	}{
		{"neon style", "postgres://user:" + secret + "@ep-cool-bird.ap-south-1.aws.neon.tech/shard0?sslmode=require"},
		{"postgresql scheme", "postgresql://user:" + secret + "@localhost:5432/db"},
		{"password with symbols", "postgres://user:" + secret + "%21%40@host:5432/db"},
		{"keyword form", "host=localhost user=postgres password=" + secret + " dbname=shard0"},
		{"unparseable", "postgres://user:" + secret + "@host:not-a-port/db"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactDSN(tc.dsn)
			if got == "" {
				t.Fatal("redactDSN returned empty string")
			}
			if contains(got, secret) {
				t.Errorf("password leaked in %q", got)
			}
		})
	}
}

func TestRedactDSNKeepsHostForDiagnosis(t *testing.T) {
	got := redactDSN("postgres://user:pw@ep-cool-bird.aws.neon.tech:5432/shard0")
	if !contains(got, "ep-cool-bird.aws.neon.tech") {
		t.Errorf("host should survive redaction for diagnosis, got %q", got)
	}
	if contains(got, "pw@") {
		t.Errorf("password survived: %q", got)
	}
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
