package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnpackBuildArchive(t *testing.T) {
	cases := []struct {
		name      string
		headers   []*tar.Header
		wantError bool
	}{
		{"nested Dockerfile", []*tar.Header{
			{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": strings.Repeat("a", 40)}},
			{Name: "repo/app/Dockerfile", Typeflag: tar.TypeReg, Size: 4, Mode: 0o755},
		}, false},
		{"parent traversal", []*tar.Header{{Name: "repo/../outside", Typeflag: tar.TypeReg}}, true},
		{"absolute symlink", []*tar.Header{{Name: "repo/app", Typeflag: tar.TypeSymlink, Linkname: "/tmp"}}, true},
		{"escaping symlink", []*tar.Header{{Name: "repo/app", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}}, true},
		{"mixed archive roots", []*tar.Header{{Name: "repo/app", Typeflag: tar.TypeDir}, {Name: "other/app", Typeflag: tar.TypeDir}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			a := tar.NewWriter(gz)
			for _, h := range tc.headers {
				if err := a.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if h.Size > 0 {
					if _, err := a.Write([]byte("test")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			err := unpackBuildArchive(&buf, dir)
			if (err != nil) != tc.wantError {
				t.Fatalf("archive error = %v, want error %t", err, tc.wantError)
			}
			if !tc.wantError {
				file := filepath.Join(dir, "app", "Dockerfile")
				data, err := os.ReadFile(file)
				if err != nil || string(data) != "test" {
					t.Fatalf("Dockerfile: %q, %v", data, err)
				}
				info, err := os.Stat(file)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o755 {
					t.Fatalf("mode = %v", info.Mode())
				}
			}
		})
	}
}

func TestKubePreviewHostIsBoundedAndSeparatesSlugs(t *testing.T) {
	ref := PreviewRef{Repo: "one/" + strings.Repeat("long-", 20) + "x_y", PR: 98765}
	first := kubePreviewHost(ref, "hatch.example.org")
	ref.Repo = strings.ReplaceAll(ref.Repo, "_", "-")
	second := kubePreviewHost(ref, "hatch.example.org")
	label, _, _ := strings.Cut(first, ".")
	if len(label) > 63 || first == second || !strings.HasPrefix(first, "pr-98765-") {
		t.Fatalf("unexpected hosts %s and %s", first, second)
	}
}
