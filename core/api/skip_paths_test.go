package main

import "testing"

func TestMatchSkipGlob(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/*.md", "README.md", true},
		{"**/*.md", "docs/guide.md", true},
		{"**/*.md", "docs/sub/guide.md", true},
		{"**/*.md", "src/handler.go", false},
		{"docs/**", "docs/intro.txt", true},
		{"docs/**", "docs/sub/deep/file.txt", true},
		{"docs/**", "src/docs.go", false},
		{".github/**", ".github/workflows/ci.yml", true},
		{"LICENSE*", "LICENSE", true},
		{"LICENSE*", "LICENSE.txt", true},
		{"LICENSE*", "src/license.go", false},
		{".gitignore", ".gitignore", true},
		{".gitignore", "subdir/.gitignore", false}, // exact, no **
		{"**/*.pdf", "report.pdf", true},
		{"**/*.pdf", "deep/path/to/report.pdf", true},
		// Case-insensitive.
		{"**/*.md", "Readme.MD", true},
		{"LICENSE*", "license", true},
	}
	for _, c := range cases {
		got := matchSkipGlob(c.pattern, c.path)
		if got != c.want {
			t.Errorf("matchSkipGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestAllFilesMatchSkip(t *testing.T) {
	defaults := defaultSkipPaths

	cases := []struct {
		name  string
		files []string
		want  bool
	}{
		{
			name:  "all md files",
			files: []string{"README.md", "docs/install.md"},
			want:  true,
		},
		{
			name:  "mixed code + md",
			files: []string{"README.md", "src/main.go"},
			want:  false,
		},
		{
			name:  "only LICENSE + .gitignore",
			files: []string{"LICENSE", ".gitignore"},
			want:  true,
		},
		{
			name:  "only github workflows",
			files: []string{".github/workflows/ci.yml", ".github/dependabot.yml"},
			want:  true,
		},
		{
			name:  "empty list never skips",
			files: nil,
			want:  false,
		},
		{
			name:  "pdf only",
			files: []string{"reports/q1.pdf", "doc.pdf"},
			want:  true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := allFilesMatchSkip(c.files, defaults)
			if got != c.want {
				t.Fatalf("allFilesMatchSkip(%v) = %v, want %v", c.files, got, c.want)
			}
		})
	}
}
