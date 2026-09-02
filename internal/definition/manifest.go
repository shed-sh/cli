package definition

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	ManifestAPIVersion = "shed.run/v1alpha1"
	ManifestKind       = "Application"
	ManifestFileName   = "SHED.yaml"
)

// WorkloadKind is the shed's workload identity as the control plane names it.
// The document kind carries it: an Application is an app. Later document
// kinds (Image, Worker, StaticSite) map to their own values here.
func (m Manifest) WorkloadKind() string {
	return "app"
}

// BundleKind is the shipping format this manifest's bundle uses on the wire.
// Applications ship source for the remote builder.
func (m Manifest) BundleKind() string {
	return "source"
}

// Manifest is the complete, portable contract between project detection and
// execution. Builders must not infer anything from the original source tree.
type Manifest struct {
	APIVersion string                  `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                  `yaml:"kind" json:"kind"`
	Metadata   *ManifestMetadata       `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Content    ManifestContent         `yaml:"content" json:"content"`
	Build      ManifestBuild           `yaml:"build" json:"build"`
	Run        ManifestRun             `yaml:"run" json:"run"`
	Base       string                  `yaml:"base,omitempty" json:"base,omitempty"`
	Parts      map[string]ManifestPart `yaml:"parts,omitempty" json:"parts,omitempty"`
	Apps       map[string]ManifestApp  `yaml:"apps,omitempty" json:"apps,omitempty"`
}

type ManifestMetadata struct {
	Name string `yaml:"name" json:"name"`
}

// Project names become hostname segments composed with a deployment
// short-id and tenant slug into one 63-character DNS label, so the name's
// own budget is 30 — matching the control plane's validator exactly, so a
// bad name fails here instead of as a server 400.
var projectNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,28}[a-z0-9])?$`)

// ValidProjectName reports whether name fits that DNS-label budget. It exists
// so callers that validate a name at its authoring position — the SHED
// evaluator — apply exactly the rule Marshal will enforce later.
func ValidProjectName(name string) bool {
	return projectNamePattern.MatchString(name)
}

type ManifestContent struct {
	Include []string `yaml:"include" json:"include"`
}

type ManifestBuild struct {
	Image    string     `yaml:"image" json:"image"`
	Commands [][]string `yaml:"commands,omitempty" json:"commands,omitempty"`
}

type ManifestRun struct {
	Command          []string          `yaml:"command" json:"command"`
	WorkingDirectory string            `yaml:"workingDirectory,omitempty" json:"workingDirectory,omitempty"`
	User             string            `yaml:"user,omitempty" json:"user,omitempty"`
	Environment      map[string]string `yaml:"environment,omitempty" json:"environment,omitempty"`
	Port             int               `yaml:"port" json:"port"`
	StopSignal       string            `yaml:"stopSignal,omitempty" json:"stopSignal,omitempty"`
}

// ManifestPart and ManifestApp are the trusted remote-builder projection of
// the same detected application. Local execution owns build/run; the remote
// builder owns base/parts/apps. Keeping both in one generated document lets
// every consumer parse only its section without redetecting the source tree.
type ManifestPart struct {
	Plugin       string               `yaml:"plugin" json:"plugin"`
	Source       string               `yaml:"source" json:"source"`
	Dependencies ManifestDependencies `yaml:"dependencies" json:"dependencies"`
	Stage        []string             `yaml:"stage" json:"stage"`
	Prime        []string             `yaml:"prime" json:"prime"`
}

type ManifestDependencies struct {
	Manager string   `yaml:"manager" json:"manager"`
	Inputs  []string `yaml:"inputs" json:"inputs"`
}

type ManifestApp struct {
	Command          []string          `yaml:"command" json:"command"`
	Args             []string          `yaml:"args,omitempty" json:"args,omitempty"`
	WorkingDirectory string            `yaml:"working-directory,omitempty" json:"workingDirectory,omitempty"`
	User             string            `yaml:"user,omitempty" json:"user,omitempty"`
	Environment      map[string]string `yaml:"environment,omitempty" json:"environment,omitempty"`
	Ports            []string          `yaml:"ports,omitempty" json:"ports,omitempty"`
	StopSignal       string            `yaml:"stop-signal,omitempty" json:"stopSignal,omitempty"`
}

func (m Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", ManifestFileName, err)
	}
	return data, nil
}

func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode %s: %w", ManifestFileName, err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if m.APIVersion != ManifestAPIVersion {
		return fmt.Errorf("apiVersion must be %q", ManifestAPIVersion)
	}
	if m.Kind != ManifestKind {
		return fmt.Errorf("kind must be %q", ManifestKind)
	}
	if m.Metadata != nil && !projectNamePattern.MatchString(m.Metadata.Name) {
		return errors.New("metadata.name must be a lowercase DNS label of at most 30 characters")
	}
	if len(m.Content.Include) == 0 {
		return errors.New("content.include must not be empty")
	}
	if err := validatePathSet(m.Content.Include); err != nil {
		return fmt.Errorf("content.include: %w", err)
	}
	if strings.TrimSpace(m.Build.Image) == "" || strings.ContainsAny(m.Build.Image, "\r\n\x00") {
		return errors.New("build.image must be a non-empty single-line image reference")
	}
	for _, command := range m.Build.Commands {
		if err := validateCommand(command); err != nil {
			return fmt.Errorf("build command: %w", err)
		}
	}
	if err := validateCommand(m.Run.Command); err != nil {
		return fmt.Errorf("run.command: %w", err)
	}
	if m.Run.Port < 1 || m.Run.Port > 65535 {
		return fmt.Errorf("run.port must be between 1 and 65535")
	}
	// These three are concatenated verbatim into generated Dockerfile
	// directives, so a line break in any of them would smuggle in extra
	// directives that no field of the manifest declares.
	if strings.ContainsAny(m.Run.WorkingDirectory, "\r\n\x00") {
		return errors.New("run.workingDirectory must be a single-line path")
	}
	if strings.ContainsAny(m.Run.User, " \t\r\n\x00") {
		return errors.New("run.user must be a single-line user or user:group reference")
	}
	if strings.ContainsAny(m.Run.StopSignal, " \t\r\n\x00") {
		return errors.New("run.stopSignal must be a single signal name")
	}
	for key, value := range m.Run.Environment {
		if key == "" || strings.ContainsAny(key, "= \t\r\n\x00") || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("run.environment contains an invalid entry")
		}
	}
	return nil
}

func validateCommand(command []string) error {
	if len(command) == 0 {
		return errors.New("command must not be empty")
	}
	for _, value := range command {
		if value == "" || strings.ContainsRune(value, '\x00') {
			return errors.New("arguments must be non-empty and contain no NUL")
		}
	}
	return nil
}

func validatePathSet(values []string) error {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	for index, value := range sorted {
		if value == "" || value == "." || path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, `\`) || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("%q is not a normalized relative path", value)
		}
		for _, segment := range strings.Split(value, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return fmt.Errorf("%q contains an unsafe path segment", value)
			}
		}
		if index > 0 && (value == sorted[index-1] || strings.HasPrefix(value, sorted[index-1]+"/")) {
			return fmt.Errorf("paths %q and %q overlap", sorted[index-1], value)
		}
	}
	return nil
}
