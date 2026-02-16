package tagging

import (
	"reflect"
	"testing"
)

func TestExtractHashtags(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "extracts hashtags from content",
			content: "Write #Work notes and #planning docs",
			want:    []string{"work", "planning"},
		},
		{
			name:    "deduplicates hashtags case insensitively",
			content: "#Work #work #WORK",
			want:    []string{"work"},
		},
		{
			name:    "supports alphanumeric hashtag body",
			content: "roadmap #q1planning2026 #v2",
			want:    []string{"q1planning2026", "v2"},
		},
		{
			name:    "handles punctuation and start end boundaries",
			content: "#start then mid #middle, and end #finish",
			want:    []string{"start", "middle", "finish"},
		},
		{
			name:    "handles special patterns predictably",
			content: "ignore#inline #-bad #_bad #123 #ok-tag #good",
			want:    []string{"ok", "good"},
		},
		{
			name:    "returns empty for no hashtags",
			content: "plain text only",
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractHashtags(tt.content)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ExtractHashtags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectGeneratedTags(t *testing.T) {
	generated := []string{" work ", "newtag", "WORK", "misc"}
	existingNoteTags := map[string]bool{
		"already": true,
	}
	existingTagNames := map[string]bool{
		"work": true,
	}

	got := SelectGeneratedTags(generated, existingNoteTags, existingTagNames, 3)
	want := []string{"work", "newtag", "misc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SelectGeneratedTags() = %v, want %v", got, want)
	}
}

func TestSelectHashtagsToAdd(t *testing.T) {
	existingNoteTags := map[string]bool{
		"work": true,
	}

	got := SelectHashtagsToAdd("#Work #new #other", existingNoteTags, 1)
	want := []string{"new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SelectHashtagsToAdd() = %v, want %v", got, want)
	}
}
