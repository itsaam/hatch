package main

import "testing"

func TestParseMention(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		bot     string
		want    commentVerb
		wantHit bool
	}{
		{
			name:    "bare mention is ignored",
			body:    "@hatchpr",
			bot:     "hatchpr",
			want:    verbNone,
			wantHit: false,
		},
		{
			name:    "mention preview -> deploy",
			body:    "@hatchpr preview",
			bot:     "hatchpr",
			want:    verbDeploy,
			wantHit: true,
		},
		{
			name:    "deploy alias is ignored",
			body:    "@hatchpr deploy",
			bot:     "hatchpr",
			want:    verbNone,
			wantHit: false,
		},
		{
			name:    "mention delete -> destroy",
			body:    "@hatchpr delete",
			bot:     "hatchpr",
			want:    verbDestroy,
			wantHit: true,
		},
		{
			name:    "mention destroy -> destroy",
			body:    "@hatchpr destroy",
			bot:     "hatchpr",
			want:    verbDestroy,
			wantHit: true,
		},
		{
			name:    "case insensitive",
			body:    "Hey @HatchPR DELETE please",
			bot:     "hatchpr",
			want:    verbDestroy,
			wantHit: true,
		},
		{
			name:    "no mention",
			body:    "this PR is great",
			bot:     "hatchpr",
			want:    verbNone,
			wantHit: false,
		},
		{
			name:    "email-like is not a mention",
			body:    "ping me at user@hatchpr.dev",
			bot:     "hatchpr",
			want:    verbNone,
			wantHit: false,
		},
		{
			name:    "mention with leading text",
			body:    "Please @hatchpr preview this!",
			bot:     "hatchpr",
			want:    verbDeploy,
			wantHit: true,
		},
		{
			name:    "unknown verb is ignored",
			body:    "@hatchpr please?",
			bot:     "hatchpr",
			want:    verbNone,
			wantHit: false,
		},
		{
			name:    "trailing punctuation stripped",
			body:    "@hatchpr destroy.",
			bot:     "hatchpr",
			want:    verbDestroy,
			wantHit: true,
		},
		{
			name: "lookalike handle is ignored",
			body: "@hatchpr-preview preview",
			bot:  "hatchpr",
		},
		{
			name:    "different bot handle",
			body:    "@hatchpr deploy",
			bot:     "otherbot",
			want:    verbNone,
			wantHit: false,
		},
		{
			name:    "empty bot",
			body:    "@hatchpr deploy",
			bot:     "",
			want:    verbNone,
			wantHit: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, hit := parseMention(c.body, c.bot)
			if hit != c.wantHit || got != c.want {
				t.Fatalf("parseMention(%q, %q) = (%d, %v), want (%d, %v)",
					c.body, c.bot, got, hit, c.want, c.wantHit)
			}
		})
	}
}
