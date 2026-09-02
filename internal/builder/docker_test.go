package builder

import (
	"strings"
	"testing"

	"shed/internal/definition"
)

func TestDockerfileIsDerivedOnlyFromManifest(t *testing.T) {
	manifest := definition.Manifest{
		APIVersion: definition.ManifestAPIVersion,
		Kind:       definition.ManifestKind,
		Content:    definition.ManifestContent{Include: []string{"package.json", "src"}},
		Build:      definition.ManifestBuild{Image: "node:24", Commands: [][]string{{"npm", "ci"}, {"npm", "run", "build"}}},
		Run: definition.ManifestRun{
			Command: []string{"node", "server.js"}, Environment: map[string]string{"PORT": "3000", "NODE_ENV": "production"}, Port: 3000, User: "1000:1000",
		},
	}
	first := dockerfileFor(manifest)
	second := dockerfileFor(manifest)
	if first != second {
		t.Fatal("Dockerfile generation is not deterministic")
	}
	for _, expected := range []string{"FROM node:24", `RUN ["npm","ci"]`, `RUN ["npm","run","build"]`, "EXPOSE 3000", `CMD ["node","server.js"]`} {
		if !strings.Contains(first, expected) {
			t.Fatalf("missing %q in:\n%s", expected, first)
		}
	}
}
