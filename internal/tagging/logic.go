package tagging

import (
	"regexp"
	"strings"
)

var hashtagRegex = regexp.MustCompile(`(?:^|\s)#([a-zA-Z][a-zA-Z0-9]*)`)

// BuildExistingTagContext normalizes tag names and returns both a set and an ordered list.
func BuildExistingTagContext(tags []string) (map[string]bool, []string) {
	existingTagNames := make(map[string]bool, len(tags))
	existingTagList := make([]string, 0, len(tags))
	for _, tag := range tags {
		lowerName := strings.ToLower(strings.TrimSpace(tag))
		if lowerName == "" || existingTagNames[lowerName] {
			continue
		}
		existingTagNames[lowerName] = true
		existingTagList = append(existingTagList, lowerName)
	}
	return existingTagNames, existingTagList
}

// BuildExistingTagSet normalizes tag names and returns them as a deduplicated set.
func BuildExistingTagSet(tags []string) map[string]bool {
	existing := make(map[string]bool, len(tags))
	for _, tag := range tags {
		name := strings.ToLower(strings.TrimSpace(tag))
		if name == "" {
			continue
		}
		existing[name] = true
	}
	return existing
}

// ExtractHashtags extracts hashtags from note content and returns them as lowercase tag names.
func ExtractHashtags(content string) []string {
	matches := hashtagRegex.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	tags := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		tag := strings.ToLower(match[1])
		if seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	return tags
}

// SelectHashtagsToAdd returns new hashtags from content up to maxNewTags.
func SelectHashtagsToAdd(content string, existingNoteTagNames map[string]bool, maxNewTags int) []string {
	if maxNewTags <= 0 {
		return nil
	}

	hashtags := ExtractHashtags(content)
	tagsToAdd := make([]string, 0, len(hashtags))
	for _, ht := range hashtags {
		if existingNoteTagNames[ht] {
			continue
		}
		tagsToAdd = append(tagsToAdd, ht)
		existingNoteTagNames[ht] = true
		if len(tagsToAdd) >= maxNewTags {
			break
		}
	}
	return tagsToAdd
}

// SelectGeneratedTags prioritizes existing tags and returns up to maxNewTags new tags.
func SelectGeneratedTags(generatedTags []string, existingNoteTagNames map[string]bool, existingTagNames map[string]bool, maxNewTags int) []string {
	if maxNewTags <= 0 {
		return nil
	}

	preferredTags := make([]string, 0, len(generatedTags))
	otherTags := make([]string, 0, len(generatedTags))

	for _, tag := range generatedTags {
		normalized := strings.ToLower(strings.TrimSpace(tag))
		if normalized == "" || existingNoteTagNames[normalized] {
			continue
		}
		existingNoteTagNames[normalized] = true
		if existingTagNames[normalized] {
			preferredTags = append(preferredTags, normalized)
		} else {
			otherTags = append(otherTags, normalized)
		}
	}

	newTags := append(preferredTags, otherTags...)
	if len(newTags) > maxNewTags {
		newTags = newTags[:maxNewTags]
	}
	return newTags
}
