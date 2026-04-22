package main

import (
	"reflect"
	"testing"
)

func TestParseBaseImages(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "simple single stage",
			in:   "FROM node:20-alpine\nRUN echo hi\n",
			want: []string{"node:20-alpine"},
		},
		{
			name: "multi-stage with AS alias excluded",
			in: `
FROM golang:1.23-alpine AS build
RUN go build -o /out/api
FROM alpine:3.19
COPY --from=build /out/api /app/api
FROM build
`,
			want: []string{"golang:1.23-alpine", "alpine:3.19"},
		},
		{
			name: "platform flag stripped",
			in:   "FROM --platform=linux/amd64 node:20-alpine AS app\n",
			want: []string{"node:20-alpine"},
		},
		{
			name: "comments and blank lines ignored",
			in:   "# syntax=docker/dockerfile:1.6\n\n# build stage\nFROM python:3.12\n",
			want: []string{"python:3.12"},
		},
		{
			name: "duplicate FROM deduped",
			in:   "FROM node:20\nFROM node:20\n",
			want: []string{"node:20"},
		},
		{
			name: "variable FROM skipped",
			in:   "ARG BASE=node:20\nFROM ${BASE}\n",
			want: nil,
		},
		{
			name: "registry with port and tag",
			in:   "FROM ghcr.io:5000/foo/bar:v1.2.3\n",
			want: []string{"ghcr.io:5000/foo/bar:v1.2.3"},
		},
		{
			name: "no FROM → empty",
			in:   "RUN echo hi\n",
			want: nil,
		},
		{
			name: "lowercase from keyword",
			in:   "from node:20\n",
			want: []string{"node:20"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseBaseImages([]byte(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseBaseImages = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveDockerfilePath(t *testing.T) {
	tests := []struct {
		ctx, df, want string
	}{
		{"", "", "Dockerfile"},
		{".", "", "Dockerfile"},
		{"./", "Dockerfile", "Dockerfile"},
		{"apps/web", "", "apps/web/Dockerfile"},
		{"apps/web", "Dockerfile.prod", "apps/web/Dockerfile.prod"},
		{"apps/web", "docker/Dockerfile", "docker/Dockerfile"}, // explicit path wins
		{"./apps/web", "Dockerfile", "apps/web/Dockerfile"},
	}
	for _, tc := range tests {
		t.Run(tc.ctx+"|"+tc.df, func(t *testing.T) {
			got := resolveDockerfilePath(tc.ctx, tc.df)
			if got != tc.want {
				t.Fatalf("resolveDockerfilePath(%q,%q) = %q, want %q", tc.ctx, tc.df, got, tc.want)
			}
		})
	}
}
