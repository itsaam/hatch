package main

import (
	"errors"
	"strings"
	"testing"
)

func TestScrubString_HidesGitHubTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		mustNot []string // substrings that MUST disappear
	}{
		{
			name:    "plain clone URL",
			in:      `remote https://x-access-token:ghs_ABCDEFGHIJKLMNOP0123@github.com/foo/bar.git`,
			mustNot: []string{"ghs_ABCDEFGHIJKLMNOP0123", "x-access-token:ghs_"},
		},
		{
			name:    "url-encoded clone URL (http.Client error form)",
			in:      `Post "http://docker/v1.43/build?remote=https%3A%2F%2Fx-access-token%3Aghs_ABCDEFGHIJKLMNOP0123%40github.com%2Ffoo%2Fbar.git"`,
			mustNot: []string{"ghs_ABCDEFGHIJKLMNOP0123"},
		},
		{
			name:    "raw ghs token in prose",
			in:      `token was ghs_ABCDEFGHIJKLMNOP0123XYZ at runtime`,
			mustNot: []string{"ghs_ABCDEFGHIJKLMNOP0123XYZ"},
		},
		{
			name:    "github_pat_ token",
			in:      `got github_pat_11AAAAAA0ABcdEFghIjklmnOPQ back`,
			mustNot: []string{"github_pat_11AAAAAA0ABcdEFghIjklmnOPQ"},
		},
		{
			name:    "ghu_ OAuth user token",
			in:      `bearer ghu_ABCDEFGHIJKLMNOP1234567890 used`,
			mustNot: []string{"ghu_ABCDEFGHIJKLMNOP1234567890"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := scrubString(tc.in)
			for _, s := range tc.mustNot {
				if strings.Contains(out, s) {
					t.Fatalf("expected token to be redacted, still present: %q\nin: %q\nout: %q", s, tc.in, out)
				}
			}
			if !strings.Contains(out, "REDACTED") {
				t.Fatalf("expected REDACTED marker somewhere in output: %q", out)
			}
		})
	}
}

func TestScrubString_PreservesSafeContent(t *testing.T) {
	t.Parallel()
	in := "build http 500: remote not found github.com/foo/bar.git"
	if got := scrubString(in); got != in {
		t.Fatalf("safe content was modified:\nin:  %q\nout: %q", in, got)
	}
}

func TestScrubToken_NilSafe(t *testing.T) {
	t.Parallel()
	if got := scrubToken(nil); got != nil {
		t.Fatalf("scrubToken(nil) should return nil, got %v", got)
	}
}

func TestScrubToken_WrapsStdError(t *testing.T) {
	t.Parallel()
	err := errors.New("clone failed at https://x-access-token:ghs_XYZ1234567890ABCDEFG@github.com/o/r")
	got := scrubToken(err)
	if strings.Contains(got.Error(), "ghs_XYZ1234567890ABCDEFG") {
		t.Fatalf("token leaked after scrub: %v", got)
	}
}
