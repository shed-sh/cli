package definition

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

const (
	ManifestAPIVersion = "shed.run/v1alpha1"
	ManifestKind       = "Application"
	ManifestFileName   = "SHED.hcl"
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
//
// On disk it is SHED.hcl: one `application "<name>" { … }` block. The block
// type is the document kind and the label is the name, so APIVersion, Kind,
// and Metadata carry no attributes of their own in the file; the parser fills
// them in, and the JSON form (the wire contract) still spells them out.
type Manifest struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Metadata   *ManifestMetadata       `json:"metadata,omitempty"`
	Content    ManifestContent         `json:"content"`
	Build      ManifestBuild           `json:"build"`
	Run        ManifestRun             `json:"run"`
	Base       string                  `json:"base,omitempty"`
	Parts      map[string]ManifestPart `json:"parts,omitempty"`
	Apps       map[string]ManifestApp  `json:"apps,omitempty"`
}

type ManifestMetadata struct {
	Name string `json:"name"`
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
	Include []string `json:"include"`
}

type ManifestBuild struct {
	Image    string     `json:"image"`
	Commands [][]string `json:"commands,omitempty"`
}

type ManifestRun struct {
	Command          []string          `json:"command"`
	WorkingDirectory string            `json:"workingDirectory,omitempty"`
	User             string            `json:"user,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	Port             int               `json:"port"`
	StopSignal       string            `json:"stopSignal,omitempty"`
}

// ManifestPart and ManifestApp are the trusted remote-builder projection of
// the same detected application. Local execution owns build/run; the remote
// builder owns base/parts/apps. Keeping both in one generated document lets
// every consumer parse only its section without redetecting the source tree.
type ManifestPart struct {
	Plugin       string               `json:"plugin"`
	Source       string               `json:"source"`
	Dependencies ManifestDependencies `json:"dependencies"`
	Stage        []string             `json:"stage"`
	Prime        []string             `json:"prime"`
}

type ManifestDependencies struct {
	Manager string   `json:"manager"`
	Inputs  []string `json:"inputs"`
}

type ManifestApp struct {
	Command          []string          `json:"command"`
	Args             []string          `json:"args,omitempty"`
	WorkingDirectory string            `json:"workingDirectory,omitempty"`
	User             string            `json:"user,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	Ports            []string          `json:"ports,omitempty"`
	StopSignal       string            `json:"stopSignal,omitempty"`
}

// The hcl* types are the file's shape as gohcl decodes it. They exist apart
// from Manifest so the on-disk spelling (snake_case attributes, labeled
// blocks) can differ from the wire spelling (camelCase JSON) without either
// leaking into the other. gohcl is strict: an attribute or block the schema
// does not declare is a diagnostic, which is what makes typos fail loudly.
type hclFile struct {
	Applications []hclApplication `hcl:"application,block"`
}

type hclApplication struct {
	Name    string     `hcl:"name,label"`
	Content hclContent `hcl:"content,block"`
	Build   hclBuild   `hcl:"build,block"`
	Run     hclRun     `hcl:"run,block"`
	Base    string     `hcl:"base,optional"`
	Parts   []hclPart  `hcl:"part,block"`
	Apps    []hclApp   `hcl:"app,block"`
}

type hclContent struct {
	Include []string `hcl:"include"`
}

type hclBuild struct {
	Image    string     `hcl:"image"`
	Commands [][]string `hcl:"commands,optional"`
}

type hclRun struct {
	Command          []string          `hcl:"command"`
	Port             int               `hcl:"port"`
	WorkingDirectory string            `hcl:"working_directory,optional"`
	User             string            `hcl:"user,optional"`
	Environment      map[string]string `hcl:"environment,optional"`
	StopSignal       string            `hcl:"stop_signal,optional"`
}

type hclPart struct {
	Name         string          `hcl:"name,label"`
	Plugin       string          `hcl:"plugin"`
	Source       string          `hcl:"source"`
	Dependencies hclDependencies `hcl:"dependencies,block"`
	Stage        []string        `hcl:"stage"`
	Prime        []string        `hcl:"prime"`
}

type hclDependencies struct {
	Manager string   `hcl:"manager"`
	Inputs  []string `hcl:"inputs"`
}

type hclApp struct {
	Name             string            `hcl:"name,label"`
	Command          []string          `hcl:"command"`
	Args             []string          `hcl:"args,optional"`
	WorkingDirectory string            `hcl:"working_directory,optional"`
	User             string            `hcl:"user,optional"`
	Environment      map[string]string `hcl:"environment,optional"`
	Ports            []string          `hcl:"ports,optional"`
	StopSignal       string            `hcl:"stop_signal,optional"`
}

func (file hclFile) manifest() (Manifest, error) {
	if len(file.Applications) != 1 {
		return Manifest{}, fmt.Errorf("%s must declare exactly one application block, found %d", ManifestFileName, len(file.Applications))
	}
	app := file.Applications[0]
	manifest := Manifest{
		APIVersion: ManifestAPIVersion,
		Kind:       ManifestKind,
		Metadata:   &ManifestMetadata{Name: app.Name},
		Content:    ManifestContent{Include: app.Content.Include},
		Build:      ManifestBuild{Image: app.Build.Image, Commands: app.Build.Commands},
		Run: ManifestRun{
			Command:          app.Run.Command,
			WorkingDirectory: app.Run.WorkingDirectory,
			User:             app.Run.User,
			Environment:      app.Run.Environment,
			Port:             app.Run.Port,
			StopSignal:       app.Run.StopSignal,
		},
		Base: app.Base,
	}
	if len(app.Parts) > 0 {
		manifest.Parts = make(map[string]ManifestPart, len(app.Parts))
		for _, part := range app.Parts {
			if _, duplicate := manifest.Parts[part.Name]; duplicate {
				return Manifest{}, fmt.Errorf("part %q is declared twice", part.Name)
			}
			manifest.Parts[part.Name] = ManifestPart{
				Plugin:       part.Plugin,
				Source:       part.Source,
				Dependencies: ManifestDependencies{Manager: part.Dependencies.Manager, Inputs: part.Dependencies.Inputs},
				Stage:        part.Stage,
				Prime:        part.Prime,
			}
		}
	}
	if len(app.Apps) > 0 {
		manifest.Apps = make(map[string]ManifestApp, len(app.Apps))
		for _, application := range app.Apps {
			if _, duplicate := manifest.Apps[application.Name]; duplicate {
				return Manifest{}, fmt.Errorf("app %q is declared twice", application.Name)
			}
			manifest.Apps[application.Name] = ManifestApp{
				Command:          application.Command,
				Args:             application.Args,
				WorkingDirectory: application.WorkingDirectory,
				User:             application.User,
				Environment:      application.Environment,
				Ports:            application.Ports,
				StopSignal:       application.StopSignal,
			}
		}
	}
	return manifest, nil
}

// Marshal renders the manifest as SHED.hcl. The output is canonical: the same
// manifest always produces the same bytes, which is what lets the archive's
// embedded copy take part in the content digest.
func (m Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if m.Metadata == nil {
		return nil, fmt.Errorf("encode %s: the application block needs a name", ManifestFileName)
	}
	file := hclwrite.NewEmptyFile()
	app := file.Body().AppendNewBlock("application", []string{m.Metadata.Name}).Body()

	content := app.AppendNewBlock("content", nil).Body()
	content.SetAttributeRaw("include", stringListTokens(m.Content.Include))

	app.AppendNewline()
	build := app.AppendNewBlock("build", nil).Body()
	build.SetAttributeValue("image", cty.StringVal(m.Build.Image))
	if len(m.Build.Commands) > 0 {
		build.SetAttributeRaw("commands", commandListTokens(m.Build.Commands))
	}

	app.AppendNewline()
	run := app.AppendNewBlock("run", nil).Body()
	run.SetAttributeRaw("command", stringListTokens(m.Run.Command))
	run.SetAttributeValue("port", cty.NumberIntVal(int64(m.Run.Port)))
	setOptionalString(run, "working_directory", m.Run.WorkingDirectory)
	setOptionalString(run, "user", m.Run.User)
	setOptionalMap(run, "environment", m.Run.Environment)
	setOptionalString(run, "stop_signal", m.Run.StopSignal)

	if m.Base != "" {
		app.AppendNewline()
		app.SetAttributeValue("base", cty.StringVal(m.Base))
	}
	for _, name := range sortedKeys(m.Parts) {
		part := m.Parts[name]
		app.AppendNewline()
		body := app.AppendNewBlock("part", []string{name}).Body()
		body.SetAttributeValue("plugin", cty.StringVal(part.Plugin))
		body.SetAttributeValue("source", cty.StringVal(part.Source))
		body.SetAttributeRaw("stage", stringListTokens(part.Stage))
		body.SetAttributeRaw("prime", stringListTokens(part.Prime))
		body.AppendNewline()
		dependencies := body.AppendNewBlock("dependencies", nil).Body()
		dependencies.SetAttributeValue("manager", cty.StringVal(part.Dependencies.Manager))
		dependencies.SetAttributeRaw("inputs", stringListTokens(part.Dependencies.Inputs))
	}
	for _, name := range sortedKeys(m.Apps) {
		application := m.Apps[name]
		app.AppendNewline()
		body := app.AppendNewBlock("app", []string{name}).Body()
		body.SetAttributeRaw("command", stringListTokens(application.Command))
		if len(application.Args) > 0 {
			body.SetAttributeRaw("args", stringListTokens(application.Args))
		}
		setOptionalString(body, "working_directory", application.WorkingDirectory)
		setOptionalString(body, "user", application.User)
		setOptionalMap(body, "environment", application.Environment)
		if len(application.Ports) > 0 {
			body.SetAttributeRaw("ports", stringListTokens(application.Ports))
		}
		setOptionalString(body, "stop_signal", application.StopSignal)
	}
	return hclwrite.Format(file.Bytes()), nil
}

// ParseManifest decodes SHED.hcl. Decoding is strict — an attribute or block
// the schema does not know is an error, not a silent no-op — and the result
// passes the same validation Marshal applies.
func ParseManifest(data []byte) (Manifest, error) {
	file, diags := hclparse.NewParser().ParseHCL(data, ManifestFileName)
	if diags.HasErrors() {
		return Manifest{}, fmt.Errorf("decode %s: %w", ManifestFileName, diags)
	}
	var parsed hclFile
	if diags := gohcl.DecodeBody(file.Body, nil, &parsed); diags.HasErrors() {
		return Manifest{}, fmt.Errorf("decode %s: %w", ManifestFileName, diags)
	}
	manifest, err := parsed.manifest()
	if err != nil {
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
		return errors.New("the application name must be a lowercase DNS label of at most 30 characters")
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
		return errors.New("run.working_directory must be a single-line path")
	}
	if strings.ContainsAny(m.Run.User, " \t\r\n\x00") {
		return errors.New("run.user must be a single-line user or user:group reference")
	}
	if strings.ContainsAny(m.Run.StopSignal, " \t\r\n\x00") {
		return errors.New("run.stop_signal must be a single signal name")
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

// Rendering helpers. hclwrite prints every list on one line; past a few
// entries that stops being readable, so longer lists get one entry per line.
// Format() then takes care of the indentation.

const inlineListLimit = 3

func setOptionalString(body *hclwrite.Body, name, value string) {
	if value != "" {
		body.SetAttributeValue(name, cty.StringVal(value))
	}
}

func setOptionalMap(body *hclwrite.Body, name string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	entries := make(map[string]cty.Value, len(values))
	for key, value := range values {
		entries[key] = cty.StringVal(value)
	}
	body.SetAttributeValue(name, cty.ObjectVal(entries))
}

func stringListTokens(values []string) hclwrite.Tokens {
	items := make([]hclwrite.Tokens, len(values))
	for index, value := range values {
		items[index] = hclwrite.TokensForValue(cty.StringVal(value))
	}
	return listTokens(items, len(values) > inlineListLimit)
}

func commandListTokens(commands [][]string) hclwrite.Tokens {
	items := make([]hclwrite.Tokens, len(commands))
	for index, command := range commands {
		items[index] = stringListTokens(command)
	}
	return listTokens(items, true)
}

func listTokens(items []hclwrite.Tokens, multiline bool) hclwrite.Tokens {
	tokens := hclwrite.Tokens{{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")}}
	if multiline {
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")})
	}
	for index, item := range items {
		tokens = append(tokens, item...)
		if multiline {
			tokens = append(tokens,
				&hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(",")},
				&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")})
		} else if index < len(items)-1 {
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(",")})
		}
	}
	return append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
