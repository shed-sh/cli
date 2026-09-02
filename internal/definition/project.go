package definition

import (
	"errors"
	"path/filepath"
	"strings"
)

// ProjectName resolves the stable application identity used by remote
// deployments. Explicit input wins, followed by metadata.name and finally a
// deterministic name inferred from the application directory.
func ProjectName(root, explicit string, manifest Manifest) (string, error) {
	name := explicit
	if name == "" && manifest.Metadata != nil {
		name = manifest.Metadata.Name
	}
	if name == "" {
		name = SanitizeProjectName(filepath.Base(filepath.Clean(root)))
	}
	if !projectNamePattern.MatchString(name) {
		return "", errors.New("project name must be a lowercase DNS label of at most 30 characters")
	}
	return name, nil
}

func SanitizeProjectName(value string) string {
	var result strings.Builder
	separator := false
	for _, character := range strings.ToLower(value) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			if separator && result.Len() > 0 {
				result.WriteByte('-')
			}
			result.WriteRune(character)
			separator = false
		} else {
			separator = true
		}
		if result.Len() >= 30 {
			break
		}
	}
	name := strings.Trim(result.String(), "-")
	if name == "" {
		return "app"
	}
	return name
}
