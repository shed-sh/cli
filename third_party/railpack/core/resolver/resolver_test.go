package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesWithDefaults(t *testing.T) {
	pkg := NewRequestedPackage("node", "18")
	assert.Equal(t, "18", pkg.Version)
	assert.Equal(t, DefaultSource, pkg.Source)

	pkg.SetVersion("22", "package.json engines")
	assert.Equal(t, "22", pkg.Version)
	assert.Equal(t, "package.json engines", pkg.Source)
}

func TestPackageResolver(t *testing.T) {
	resolver := NewUnresolved()

	// Set up Node.js
	node := resolver.Default("node", "18")
	resolver.Version(node, "23", "package.json engines")

	// Set up Bun
	resolver.Default("bun", "latest")

	// Set up Go
	golang := resolver.Default("go", "1.21")
	resolver.Version(golang, "1.22", "GO_VERSION environment variable")
	resolver.Version(golang, "1.23", ".go-version")

	// Set up Python
	python := resolver.Default("python", "3.11")
	resolver.Version(python, "3.12", "PYTHON_VERSION environment variable")
	resolver.Version(python, "3.13", ".python-version")

	// Set up PHP
	php := resolver.Default("php", "7.3")
	resolver.SetVersionAvailable(php, func(version string) bool {
		return version == "7.3.27"
	})

	// Resolve all packages
	resolvedPackages, err := resolver.ResolvePackages()
	require.NoError(t, err)
	assert.Equal(t, 5, len(resolvedPackages))

	// Check Node.js resolution
	nodeResolved := resolvedPackages["node"]
	require.NotNil(t, nodeResolved)
	require.NotNil(t, nodeResolved.ResolvedVersion)
	assert.Equal(t, "23", *nodeResolved.ResolvedVersion)

	// Check Bun resolution
	bunResolved := resolvedPackages["bun"]
	assert.NotNil(t, bunResolved)

	// Check Go resolution
	goResolved := resolvedPackages["go"]
	require.NotNil(t, goResolved)
	require.NotNil(t, goResolved.ResolvedVersion)
	assert.Equal(t, "1.23", *goResolved.ResolvedVersion)

	// Check Python resolution
	pythonResolved := resolvedPackages["python"]
	require.NotNil(t, pythonResolved)
	require.NotNil(t, pythonResolved.ResolvedVersion)
	assert.Equal(t, "3.13", *pythonResolved.ResolvedVersion)

	// Check PHP resolution
	phpResolved := resolvedPackages["php"]
	require.NotNil(t, phpResolved)
	require.NotNil(t, phpResolved.ResolvedVersion)
	assert.Equal(t, "7.3", *phpResolved.ResolvedVersion)
}

func TestPackageResolverWithPreviousVersions(t *testing.T) {
	resolver := NewUnresolved()

	resolver.SetPreviousVersion("node", "16")

	// Default should use previous version
	node := resolver.Default("node", "18")
	pkg := resolver.Get("node")
	assert.Equal(t, "16", pkg.Version)
	assert.Equal(t, "previous installed version", pkg.Source)

	// Custom version should override previous version
	resolver.Version(node, "20", "manual override")
	pkg = resolver.Get("node")
	assert.Equal(t, "20", pkg.Version)
	assert.Equal(t, "manual override", pkg.Source)

	// If no previous version, default should use the requested version
	resolver.Default("go", "1.23")
	pkg = resolver.Get("go")
	assert.Equal(t, "1.23", pkg.Version)
	assert.Equal(t, DefaultSource, pkg.Source)
}

func TestResolvingPackagesNotAvailable(t *testing.T) {
	resolver := NewUnresolved()

	node := resolver.Default("node", "18.20")
	resolver.SetVersionAvailable(node, func(version string) bool {
		return version == "100"
	})

	resolved, err := resolver.ResolvePackages()
	require.NoError(t, err)
	require.Equal(t, "18.20", *resolved["node"].RequestedVersion)
}
