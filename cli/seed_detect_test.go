package main

import "testing"

func TestApplySeedDetection(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		stack   []Service
		preset  *Seed
		wantSQL string // "" = Seed must remain nil
	}{
		{
			name: "preview.sql in seed/ — preferred",
			files: map[string]string{
				"seed/preview.sql": "",
				"seed/zzz.sql":     "",
			},
			stack:   []Service{{Name: "api"}, {Name: "db"}},
			wantSQL: "./seed/preview.sql",
		},
		{
			name: "no preview.sql but any .sql in seed/ → first alphabetic",
			files: map[string]string{
				"seed/b_users.sql":   "",
				"seed/a_schema.sql":  "",
			},
			stack:   []Service{{Name: "api"}, {Name: "db"}},
			wantSQL: "./seed/a_schema.sql",
		},
		{
			name: "fallback migrations/ when no seed dir",
			files: map[string]string{
				"migrations/001_init.sql": "",
				"migrations/002_data.sql": "",
			},
			stack:   []Service{{Name: "api"}, {Name: "db"}},
			wantSQL: "./migrations/001_init.sql",
		},
		{
			name: "db/seed/preview.sql exact match",
			files: map[string]string{
				"db/seed/preview.sql":  "",
				"db/seed/other.sql":    "",
			},
			stack:   []Service{{Name: "api"}, {Name: "db"}},
			wantSQL: "./db/seed/preview.sql",
		},
		{
			name: "no db service → no seed auto-wire even if files present",
			files: map[string]string{
				"seed/preview.sql": "",
			},
			stack:   []Service{{Name: "web"}},
			wantSQL: "",
		},
		{
			name: "preset Seed is preserved (compose path won)",
			files: map[string]string{
				"seed/preview.sql": "",
			},
			stack:   []Service{{Name: "api"}, {Name: "db"}},
			preset:  &Seed{After: "db", SQL: "./existing.sql"},
			wantSQL: "./existing.sql",
		},
		{
			name: "no sql anywhere",
			files: map[string]string{
				"Dockerfile": "",
			},
			stack:   []Service{{Name: "api"}, {Name: "db"}},
			wantSQL: "",
		},
		{
			name: "ignores non-sql files in seed dir",
			files: map[string]string{
				"seed/README.md":     "",
				"seed/data.json":     "",
			},
			stack:   []Service{{Name: "api"}, {Name: "db"}},
			wantSQL: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := newMemFS(tc.files)
			r := &DetectResult{Services: tc.stack, Seed: tc.preset}
			applySeedDetection(fs, r)
			got := ""
			if r.Seed != nil {
				got = r.Seed.SQL
			}
			if got != tc.wantSQL {
				t.Fatalf("Seed.SQL = %q, want %q", got, tc.wantSQL)
			}
			if r.Seed != nil && r.Seed.After != "db" {
				t.Fatalf("Seed.After = %q, want %q", r.Seed.After, "db")
			}
		})
	}
}

// Regression: a package.json-detected Node + Postgres stack with seed/ dir
// should end up with Seed auto-wired through the public Detect() entry point.
func TestDetect_PackageJSON_WithSeedDir(t *testing.T) {
	fs := newMemFS(map[string]string{
		"package.json": `{
          "dependencies": {"pg": "^8"},
          "scripts": {"start": "node server.js"}
        }`,
		"Dockerfile":       "FROM node:20\n",
		"seed/preview.sql": "CREATE TABLE hello (id int);",
	})
	r, err := Detect(fs)
	if err != nil {
		t.Fatal(err)
	}
	if r.Seed == nil {
		t.Fatalf("want Seed auto-wired, got nil")
	}
	if r.Seed.SQL != "./seed/preview.sql" {
		t.Fatalf("Seed.SQL = %q, want ./seed/preview.sql", r.Seed.SQL)
	}
}
