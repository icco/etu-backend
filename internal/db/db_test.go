package db

import (
	"testing"
)

func TestParseTagSearch(t *testing.T) {
	tests := []struct {
		name          string
		search        string
		wantTags      []string
		wantRemaining string
	}{
		{
			name:          "single tag",
			search:        "tag:work",
			wantTags:      []string{"work"},
			wantRemaining: "",
		},
		{
			name:          "multiple tags",
			search:        "tag:work tag:urgent",
			wantTags:      []string{"work", "urgent"},
			wantRemaining: "",
		},
		{
			name:          "tag with surrounding text",
			search:        "hello tag:test world",
			wantTags:      []string{"test"},
			wantRemaining: "hello world",
		},
		{
			name:          "multiple tags with content",
			search:        "tag:foo some content tag:bar",
			wantTags:      []string{"foo", "bar"},
			wantRemaining: "some content",
		},
		{
			name:          "no tags",
			search:        "no tags here",
			wantTags:      nil,
			wantRemaining: "no tags here",
		},
		{
			name:          "empty string",
			search:        "",
			wantTags:      nil,
			wantRemaining: "",
		},
		{
			name:          "tag with numbers",
			search:        "tag:project123",
			wantTags:      []string{"project123"},
			wantRemaining: "",
		},
		{
			name:          "invalid tag format ignored",
			search:        "tag:Work tag:valid",
			wantTags:      []string{"valid"},
			wantRemaining: "tag:Work",
		},
		{
			name:          "tag at end of string",
			search:        "find this tag:important",
			wantTags:      []string{"important"},
			wantRemaining: "find this",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTags, gotRemaining := parseTagSearch(tt.search)

			if len(gotTags) != len(tt.wantTags) {
				t.Errorf("parseTagSearch() gotTags = %v, want %v", gotTags, tt.wantTags)
				return
			}
			for i := range gotTags {
				if gotTags[i] != tt.wantTags[i] {
					t.Errorf("parseTagSearch() gotTags[%d] = %v, want %v", i, gotTags[i], tt.wantTags[i])
				}
			}

			if gotRemaining != tt.wantRemaining {
				t.Errorf("parseTagSearch() gotRemaining = %q, want %q", gotRemaining, tt.wantRemaining)
			}
		})
	}
}

func TestCountWords(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{
			name:  "empty string",
			input: "",
			want:  0,
		},
		{
			name:  "whitespace only",
			input: "   \t\n  ",
			want:  0,
		},
		{
			name:  "single word",
			input: "hello",
			want:  1,
		},
		{
			name:  "multiple words",
			input: "hello world foo bar",
			want:  4,
		},
		{
			name:  "words with extra whitespace",
			input: "  hello   world  \n  foo   ",
			want:  3,
		},
		{
			name:  "words with newlines",
			input: "hello\nworld\nfoo",
			want:  3,
		},
		{
			name:  "sentence with punctuation",
			input: "Hello, world! How are you?",
			want:  5,
		},
		{
			name:  "mixed whitespace",
			input: "hello\t\tworld\n\nfoo  bar",
			want:  4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countWords(tt.input)
			if got != tt.want {
				t.Errorf("countWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
