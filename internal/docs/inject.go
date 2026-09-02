// Package docs renders the Shed skill's reference material from the clispec
// registry, so documentation is a build artifact of the command surface rather
// than a parallel document that has to be remembered.
package docs

import (
	"fmt"
	"sort"
	"strings"
)

// Markers delimit a generated region inside an otherwise hand-written file.
const (
	beginMarker = "<!-- BEGIN GENERATED: %s -->"
	endMarker   = "<!-- END GENERATED: %s -->"
)

// InjectBlocks replaces the content between each block's markers, leaving the
// surrounding prose untouched. Curated narrative and generated fact tables live
// in one file that way, each owned by whoever should own it.
//
// A missing or unbalanced marker is an error rather than an append: silently
// growing a file is how generated documentation ends up duplicated.
func InjectBlocks(content string, blocks map[string]string) (string, error) {
	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		begin := fmt.Sprintf(beginMarker, name)
		end := fmt.Sprintf(endMarker, name)

		start := strings.Index(content, begin)
		if start < 0 {
			return "", fmt.Errorf("docs: missing %q", begin)
		}
		if strings.Contains(content[start+len(begin):], begin) {
			return "", fmt.Errorf("docs: %q appears more than once", begin)
		}
		finish := strings.Index(content[start:], end)
		if finish < 0 {
			return "", fmt.Errorf("docs: %q has no matching %q", begin, end)
		}
		finish += start

		replacement := strings.TrimRight(blocks[name], "\n")
		content = content[:start+len(begin)] + "\n" + replacement + "\n" + content[finish:]
	}
	return content, nil
}
