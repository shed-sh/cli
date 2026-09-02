package core

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/railwayapp/railpack/core/app"
	c "github.com/railwayapp/railpack/core/config"
	"github.com/railwayapp/railpack/core/generate"
	"github.com/railwayapp/railpack/core/logger"
	"github.com/railwayapp/railpack/core/plan"
	"github.com/railwayapp/railpack/core/providers"
	"github.com/railwayapp/railpack/core/providers/procfile"
	"github.com/railwayapp/railpack/core/resolver"
	"github.com/railwayapp/railpack/internal/utils"
)

const (
	defaultConfigFileName = "railpack.json"
)

type GenerateBuildPlanOptions struct {
	RailpackVersion          string
	BuildCommand             string
	StartCommand             string
	PreviousVersions         map[string]string
	ConfigFilePath           string
	ErrorMissingStartCommand bool // enabled on railway
}

type BuildResult struct {
	RailpackVersion       string                               `json:"railpackVersion,omitempty"`
	Plan                  *plan.BuildPlan                      `json:"plan,omitempty"`
	ResolvedPackages      map[string]*resolver.ResolvedPackage `json:"resolvedPackages,omitempty"`
	Metadata              map[string]string                    `json:"metadata,omitempty"`
	DetectedProviders     []string                             `json:"detectedProviders,omitempty"`
	ProviderAttempts      []ProviderAttempt                    `json:"providerAttempts,omitempty"`
	ConfigurationEvidence []app.Evidence                       `json:"configurationEvidence,omitempty"`
	ProcfileEvidence      []app.Evidence                       `json:"procfileEvidence,omitempty"`
	EvidenceTruncated     bool                                 `json:"evidenceTruncated,omitempty"`
	Logs                  []logger.Msg                         `json:"logs,omitempty"`
	Success               bool                                 `json:"success,omitempty"`
}

type ProviderAttempt struct {
	Name               string         `json:"name"`
	Matched            bool           `json:"matched"`
	Selected           bool           `json:"selected"`
	DetectEvidence     []app.Evidence `json:"detectEvidence,omitempty"`
	InitializeEvidence []app.Evidence `json:"initializeEvidence,omitempty"`
	PlanEvidence       []app.Evidence `json:"planEvidence,omitempty"`
	ErrorCode          string         `json:"errorCode,omitempty"`
}

func GenerateBuildPlan(application *app.App, env *app.Environment, options *GenerateBuildPlanOptions) *BuildResult {
	return generateBuildPlan(application, env, options, generate.NewGenerateContext)
}

// GenerateBuildPlanWithResolver is the offline generation seam used by the
// vendored provider suite. Production callers use GenerateBuildPlan and retain
// normal mise resolution.
func GenerateBuildPlanWithResolver(application *app.App, env *app.Environment, options *GenerateBuildPlanOptions, packageResolver *resolver.Resolver) *BuildResult {
	return generateBuildPlan(application, env, options, func(application *app.App, environment *app.Environment, config *c.Config, logger *logger.Logger) (*generate.GenerateContext, error) {
		return generate.NewGenerateContextWithResolver(application, environment, config, logger, packageResolver)
	})
}

type generateContextFactory func(*app.App, *app.Environment, *c.Config, *logger.Logger) (*generate.GenerateContext, error)

func generateBuildPlan(application *app.App, env *app.Environment, options *GenerateBuildPlanOptions, newContext generateContextFactory) *BuildResult {
	logger := logger.NewLogger()
	if options == nil {
		options = &GenerateBuildPlanOptions{}
	}
	var providerAttempts []ProviderAttempt
	var configurationEvidence []app.Evidence
	var procfileEvidence []app.Evidence

	result := func(success bool) *BuildResult {
		return &BuildResult{
			ProviderAttempts:      providerAttempts,
			ConfigurationEvidence: configurationEvidence,
			ProcfileEvidence:      procfileEvidence,
			EvidenceTruncated:     application.EvidenceTruncated(),
			Logs:                  logger.Logs,
			Success:               success,
		}
	}

	configMark := application.Mark()
	config, err := GetConfig(application, env, options, logger)
	configurationEvidence = application.EvidenceSince(configMark)
	if err != nil {
		logger.LogError("%s", err.Error())
		return result(false)
	}

	ctx, err := newContext(application, env, config, logger)
	if err != nil {
		logger.LogError("%s", err.Error())
		return result(false)
	}

	// Set the previous versions
	if options.PreviousVersions != nil {
		for name, version := range options.PreviousVersions {
			ctx.Resolver.SetPreviousVersion(name, version)
		}
	}

	// Figure out what providers to use
	providerToUse, detectedProviderName, providerAttempts := getProviders(ctx, config)
	ctx.Metadata.Set("providers", detectedProviderName)

	// TODO: We should indicate if we have packages specified in the config
	// so that providers can determine if they should include mise in the final image (e.g. for shell script)

	if providerToUse != nil {
		mark := application.Mark()
		err = providerToUse.Plan(ctx)
		setPlanEvidence(providerAttempts, providerToUse.Name(), application.EvidenceSince(mark), err)
		if err != nil {
			logger.LogError("%s", err.Error())
			return result(false)
		}
	}

	// Run the procfile provider to support apps that have a Procfile with a start command
	procfileProvider := &procfile.ProcfileProvider{}
	procfileMark := application.Mark()
	if _, err := procfileProvider.Plan(ctx); err != nil {
		procfileEvidence = application.EvidenceSince(procfileMark)
		logger.LogError("%s", err.Error())
		return result(false)
	}
	procfileEvidence = application.EvidenceSince(procfileMark)

	// before `Generate()` any commands provided by railpack.json are *not* merged into the provider-generated
	// buildPlan. This means providers can't view any of the custom structure provided by the user via a railpack.json
	buildPlan, resolvedPackages, err := ctx.Generate()
	if err != nil {
		logger.LogError("%s", err.Error())
		return result(false)
	}

	railpackVersion := options.RailpackVersion
	if railpackVersion == "" {
		railpackVersion = "dev"
	}
	// Bake the builder version into the runtime image for observability
	buildPlan.Deploy.Variables["RAILPACK_VERSION"] = railpackVersion

	if providerToUse != nil {
		providerToUse.CleansePlan(buildPlan)
	}

	if !ValidatePlan(buildPlan, application, logger, &ValidatePlanOptions{
		ErrorMissingStartCommand: options.ErrorMissingStartCommand,
		ProviderToUse:            providerToUse,
	}) {
		return result(false)
	}

	buildResult := result(true)
	buildResult.RailpackVersion = railpackVersion
	buildResult.Plan = buildPlan
	buildResult.ResolvedPackages = resolvedPackages
	buildResult.Metadata = ctx.Metadata.Properties
	if detectedProviderName != "" {
		buildResult.DetectedProviders = []string{detectedProviderName}
	}

	return buildResult
}

// GetConfig merges the options, environment, and file config into a single config
func GetConfig(app *app.App, env *app.Environment, options *GenerateBuildPlanOptions, logger *logger.Logger) (*c.Config, error) {
	optionsConfig := GenerateConfigFromOptions(options)

	envConfig := GenerateConfigFromEnvironment(env)

	fileConfig, err := GenerateConfigFromFile(app, env, options, logger)
	if err != nil {
		return nil, err
	}

	mergedConfig := c.Merge(optionsConfig, envConfig, fileConfig)

	return mergedConfig, nil
}

func GenerateConfigFromFile(app *app.App, env *app.Environment, options *GenerateBuildPlanOptions, logger *logger.Logger) (*c.Config, error) {
	config := c.EmptyConfig()

	configFileName := defaultConfigFileName
	if options.ConfigFilePath != "" {
		configFileName = options.ConfigFilePath
	}

	if envConfigFileName, _ := env.GetConfigVariable("CONFIG_FILE"); envConfigFileName != "" {
		configFileName = envConfigFileName
	}

	if err := app.ReadJSON(configFileName, config); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// if a specific path was specified, we should indicate that it was not found and hard fail
			if configFileName != defaultConfigFileName {
				return nil, fmt.Errorf("config file %q not found", configFileName)
			}

			return config, nil
		}
		logger.LogWarn("Failed to read config file `%s`\nUse the following schema to validate your config file: %s\n", configFileName, c.SchemaUrl)
		return nil, err
	}

	logger.LogInfo("Using config file `%s`", configFileName)
	logger.LogWarn("The config file format is not yet finalized and subject to change.")

	return config, nil
}

func GenerateConfigFromEnvironment(env *app.Environment) *c.Config {
	config := c.EmptyConfig()

	if env == nil {
		return config
	}

	if installCmdVar, _ := env.GetConfigVariable("INSTALL_CMD"); installCmdVar != "" {
		installStep := config.GetOrCreateStep("install")
		installStep.Commands = []plan.Command{
			plan.NewCopyCommand("."),
			plan.NewExecShellCommand(installCmdVar, plan.ExecOptions{CustomName: installCmdVar}),
		}
	}

	if buildCmdVar, _ := env.GetConfigVariable("BUILD_CMD"); buildCmdVar != "" {
		buildStep := config.GetOrCreateStep("build")
		buildStep.Commands = []plan.Command{
			plan.NewCopyCommand("."),
			plan.NewExecShellCommand(buildCmdVar, plan.ExecOptions{CustomName: buildCmdVar}),
		}
	}

	if startCmdVar, _ := env.GetConfigVariable("START_CMD"); startCmdVar != "" {
		config.Deploy.StartCmd = startCmdVar
	}

	if packages, _ := env.GetConfigVariableList("PACKAGES"); len(packages) > 0 {
		config.Packages = utils.ParsePackageWithVersion(packages)
	}

	if aptPackages, _ := env.GetConfigVariableList("BUILD_APT_PACKAGES"); len(aptPackages) > 0 {
		config.BuildAptPackages = aptPackages
	}

	if aptPackages, _ := env.GetConfigVariableList("DEPLOY_APT_PACKAGES"); len(aptPackages) > 0 {
		config.Deploy.AptPackages = aptPackages
	}

	config.Secrets = append(config.Secrets, slices.Sorted(maps.Keys(env.Variables))...)

	return config
}

// generates a config from the CLI options
func GenerateConfigFromOptions(options *GenerateBuildPlanOptions) *c.Config {
	config := c.EmptyConfig()

	if options == nil {
		return config
	}

	if options.BuildCommand != "" {
		buildStep := config.GetOrCreateStep("build")
		buildStep.Commands = []plan.Command{
			plan.NewCopyCommand("."),
			plan.NewExecShellCommand(options.BuildCommand, plan.ExecOptions{CustomName: options.BuildCommand}),
		}
	}

	if options.StartCommand != "" {
		config.Deploy.StartCmd = options.StartCommand
	}

	return config
}

func getProviders(ctx *generate.GenerateContext, config *c.Config) (providers.Provider, string, []ProviderAttempt) {
	allProviders := providers.GetLanguageProviders()

	var providerToUse providers.Provider
	var detectedProvider string
	var attempts []ProviderAttempt

	// Even if there are providers manually specified, we want to detect to see what type of app this is
	for _, provider := range allProviders {
		attempt := ProviderAttempt{Name: provider.Name()}
		mark := ctx.App.Mark()
		matched, err := provider.Detect(ctx)
		attempt.Matched = matched
		attempt.DetectEvidence = ctx.App.EvidenceSince(mark)
		if err != nil {
			attempt.ErrorCode = app.ErrorCode(err)
			attempts = append(attempts, attempt)
			log.Warnf("Failed to detect provider `%s`: %s", provider.Name(), err.Error())
			continue
		}
		attempts = append(attempts, attempt)

		if matched {
			detectedProvider = provider.Name()

			// If there are no providers manually specified in the config,
			if config.Provider == nil {
				attempts[len(attempts)-1].Selected = true
				mark := ctx.App.Mark()
				if err := provider.Initialize(ctx); err != nil {
					attempts[len(attempts)-1].InitializeEvidence = ctx.App.EvidenceSince(mark)
					attempts[len(attempts)-1].ErrorCode = app.ErrorCode(err)
					ctx.Logger.LogWarn("Failed to initialize provider `%s`: %s", provider.Name(), err.Error())
					break
				}
				attempts[len(attempts)-1].InitializeEvidence = ctx.App.EvidenceSince(mark)

				ctx.Logger.LogInfo("Detected %s", utils.CapitalizeFirst(provider.Name()))

				providerToUse = provider
			}

			break
		}
	}

	if config.Provider != nil {
		provider := providers.GetProvider(*config.Provider)

		if provider == nil {
			attempts = append(attempts, ProviderAttempt{Name: *config.Provider, Selected: true, ErrorCode: "invalid_format"})
			ctx.Logger.LogWarn("Provider `%s` not found", *config.Provider)
			return providerToUse, detectedProvider, attempts
		}

		attemptIndex := providerAttemptIndex(attempts, provider.Name())
		if attemptIndex < 0 {
			attempts = append(attempts, ProviderAttempt{Name: provider.Name()})
			attemptIndex = len(attempts) - 1
		}
		attempts[attemptIndex].Selected = true
		mark := ctx.App.Mark()
		if err := provider.Initialize(ctx); err != nil {
			attempts[attemptIndex].InitializeEvidence = ctx.App.EvidenceSince(mark)
			attempts[attemptIndex].ErrorCode = app.ErrorCode(err)
			ctx.Logger.LogWarn("Failed to initialize provider `%s`: %s", *config.Provider, err.Error())
			return providerToUse, detectedProvider, attempts
		}
		attempts[attemptIndex].InitializeEvidence = ctx.App.EvidenceSince(mark)

		ctx.Logger.LogInfo("Using provider %s from config", utils.CapitalizeFirst(*config.Provider))
		providerToUse = provider
	}

	return providerToUse, detectedProvider, attempts
}

func providerAttemptIndex(attempts []ProviderAttempt, name string) int {
	for index := range attempts {
		if strings.EqualFold(attempts[index].Name, name) {
			return index
		}
	}
	return -1
}

func setPlanEvidence(attempts []ProviderAttempt, name string, evidence []app.Evidence, planErr error) {
	index := providerAttemptIndex(attempts, name)
	if index < 0 {
		return
	}
	attempts[index].PlanEvidence = evidence
	if planErr != nil {
		attempts[index].ErrorCode = app.ErrorCode(planErr)
	}
}
