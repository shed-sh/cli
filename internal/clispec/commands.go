package clispec

// outputFlag builds the --output flag, whose accepted values and default differ
// per command: init writes a report rather than a stream and so has no ndjson
// form, and status and cancel answer a machine by default.
func outputFlag(defaultValue string, values ...string) Flag {
	return Flag{
		Name:    "output",
		Kind:    KindString,
		Default: defaultValue,
		Summary: "Choose the output format",
		Values:  values,
	}
}

var (
	humanJSONNDJSON = []string{"human", "json", "ndjson"}
	humanJSON       = []string{"human", "json"}
)

// Groups is the command catalog. It backs dispatch, help, the JSON contract, and
// the generated reference, so a command listed here without a handler — or a
// handler without an entry — fails TestEverySpecCommandHasAHandler.
var Groups = []Group{
	{
		Title: "Your project",
		Commands: []Command{
			{
				Name:      "deploy",
				Usage:     "[directory]",
				Summary:   "Send the project to the Shed cloud, or build and run it here with --local",
				MinArgs:   0,
				MaxArgs:   1,
				UsageLine: "usage: shed deploy [directory]",
				Flags: []Flag{
					{Name: "local", Kind: KindBool, Summary: "Build and run the project in Docker on this machine instead of the cloud"},
					{Name: "dry-run", Kind: KindBool, Summary: "Inspect and package, without building, running, or uploading"},
					{Name: "mock", Kind: KindBool, Summary: "Package and stop; needs neither Docker nor the cloud"},
					{Name: "remote", Kind: KindBool, Summary: "Accepted for compatibility; the cloud is already the default"},
					{Name: "archive", Kind: KindString, Placeholder: "<path>", Summary: "Keep the generated .tar.gz at this path"},
					outputFlag("human", humanJSONNDJSON...),
					{Name: "project", Kind: KindString, Placeholder: "<name>", Summary: "Override the remote project name"},
					{Name: "request-id", Kind: KindString, Placeholder: "<id>", Summary: "Repeat a deploy without creating a second deployment"},
					{Name: "wait", Kind: KindBool, Summary: "Wait for the remote deployment to finish"},
					{Name: "detach", Kind: KindBool, Summary: "Return as soon as the cloud accepts the bundle"},
					{Name: "wait-timeout", Kind: KindDuration, Default: "30s", Positive: true, Summary: "How long to follow before detaching"},
					{Name: "non-interactive", Kind: KindBool, Summary: "Accepted for compatibility; Shed never prompts"},
				},
				Exclusive: [][]string{
					{"local", "remote"},
					{"local", "mock"},
					{"remote", "mock"},
					{"detach", "wait"},
					{"detach", "wait-timeout"},
				},
				Examples: []Example{
					{Command: "shed deploy", Comment: "Send this project to the Shed cloud"},
					{Command: "shed deploy . --output json", Comment: "The same, as one JSON result"},
					{Command: "shed deploy . --local", Comment: "Build and run it here, in Docker"},
					{Command: "shed deploy . --dry-run --archive app.tar.gz", Comment: "Package it and look inside"},
				},
			},
			{
				Name:      "init",
				Usage:     "[directory]",
				Summary:   "Look at the project and write " + DefinitionFileName + ", or SHED with --format shed",
				MinArgs:   0,
				MaxArgs:   1,
				UsageLine: "usage: shed init [directory] [--format yaml|shed] [--output human|json]",
				Flags: []Flag{
					{Name: "format", Kind: KindString, Default: "yaml", Values: []string{"yaml", "shed"}, Summary: "Definition format to write"},
					outputFlag("human", humanJSON...),
				},
			},
			{
				Name:      "check",
				Usage:     "[directory]",
				Summary:   "Evaluate the definition and report every problem in it",
				MinArgs:   0,
				MaxArgs:   1,
				UsageLine: "usage: shed check [directory] [--output human|json]",
				Flags:     []Flag{outputFlag("human", humanJSON...)},
				Examples: []Example{
					{Command: "shed check --output json", Comment: "Every diagnostic at once, while authoring"},
				},
			},
			{
				Name:      "schema",
				Summary:   "Print the SHED file API",
				UsageLine: "usage: shed schema [--output human|json]",
				Flags:     []Flag{outputFlag("human", humanJSON...)},
			},
		},
	},
	{
		Title: "Running deployments",
		Commands: []Command{
			{
				Name:      "status",
				Usage:     "<deployment>",
				Summary:   "Show, or wait for, a deployment's state",
				MinArgs:   1,
				MaxArgs:   1,
				UsageLine: "usage: shed status <deployment-id> [--wait] [--wait-timeout D]",
				Flags: []Flag{
					{Name: "wait", Kind: KindBool, Summary: "Wait for a terminal deployment state"},
					{Name: "wait-timeout", Kind: KindDuration, Default: "30s", Positive: true, Summary: "How long to follow before detaching"},
					outputFlag("json", humanJSONNDJSON...),
				},
				Examples: []Example{
					{Command: "shed status <deployment> --wait --output ndjson", Comment: "Follow it to a final state"},
				},
			},
			{
				Name:      "logs",
				Usage:     "<deployment>",
				Summary:   "Show build and runtime logs",
				MinArgs:   1,
				MaxArgs:   1,
				UsageLine: "usage: shed logs <deployment-id> [--stage system|build|runtime|all] [--follow] [--cursor C]",
				Flags: []Flag{
					{Name: "stage", Kind: KindString, Default: "all", Values: []string{"system", "build", "runtime", "all"}, Summary: "Limit records to one pipeline stage"},
					{Name: "follow", Kind: KindBool, Summary: "Keep streaming new records"},
					{Name: "cursor", Kind: KindString, Placeholder: "<cursor>", Summary: "Resume after a stream cursor"},
					outputFlag("human", humanJSONNDJSON...),
				},
			},
			{
				Name:      "open",
				Usage:     "<url>",
				Summary:   "Open a deployment in your browser",
				MinArgs:   1,
				MaxArgs:   1,
				UsageLine: "usage: shed open <url>",
			},
			{
				Name:      "stop",
				Usage:     "<instance>",
				Summary:   "Stop a local instance",
				MinArgs:   1,
				MaxArgs:   1,
				UsageLine: "usage: shed stop <instance-id>",
			},
			{
				Name:      "destroy",
				Usage:     "<instance>",
				Summary:   "Stop a local instance and forget it",
				MinArgs:   1,
				MaxArgs:   1,
				UsageLine: "usage: shed destroy <instance-id>",
			},
			{
				Name:      "cancel",
				Usage:     "<deployment>",
				Summary:   "Ask the cloud to cancel a deployment",
				MinArgs:   1,
				MaxArgs:   1,
				UsageLine: "usage: shed cancel <deployment-id>",
				Flags:     []Flag{outputFlag("json", humanJSON...)},
			},
		},
	},
	{
		Title: "Sharing",
		Commands: []Command{
			{
				Name:      "share",
				Usage:     "<deployment> <email>",
				Summary:   "Give someone access",
				MinArgs:   2,
				MaxArgs:   2,
				UsageLine: "usage: shed share <deployment-id> <email>",
			},
			{
				Name:      "revoke",
				Usage:     "<deployment> <email>",
				Summary:   "Take someone's access away",
				MinArgs:   2,
				MaxArgs:   2,
				UsageLine: "usage: shed revoke <deployment-id> <email>",
			},
		},
	},
	{
		Title: "Your account",
		Commands: []Command{
			{
				Name:      "login",
				Summary:   "Sign in to Shed",
				UsageLine: "usage: shed login [--token <pat> | --no-browser] [--name <label>]",
				Flags: []Flag{
					{Name: "token", Kind: KindString, Placeholder: "<pat>", Summary: "Sign in with a personal access token (skips the browser flow)"},
					{Name: "no-browser", Kind: KindBool, Summary: "Print the login URL instead of opening a browser"},
					{Name: "name", Kind: KindString, Placeholder: "<label>", Summary: "Name shown for this CLI token"},
				},
			},
			{
				Name:      "logout",
				Summary:   "Sign out and revoke this machine's token",
				UsageLine: "usage: shed logout [--local]",
				Flags: []Flag{
					{Name: "local", Kind: KindBool, Summary: "Remove local credentials without revoking the token"},
				},
			},
			{
				Name:      "whoami",
				Summary:   "Show who you are signed in as",
				UsageLine: "usage: shed whoami",
			},
		},
	},
	{
		Title: "About",
		Commands: []Command{
			{
				Name:      "upgrade",
				Aliases:   []string{"update"},
				Summary:   "Replace this binary with a newer released one",
				UsageLine: "usage: shed upgrade [--version <v>] [--check]",
				Flags: []Flag{
					{Name: "version", Kind: KindString, Placeholder: "<v>", Summary: "Switch to exactly this release instead of the latest"},
					{Name: "check", Kind: KindBool, Summary: "Report whether an upgrade is available, without installing"},
					outputFlag("human", humanJSON...),
				},
				Examples: []Example{
					{Command: "shed upgrade", Comment: "Move to the latest release"},
					{Command: "shed upgrade --check --output json", Comment: "Just ask, one JSON result"},
				},
			},
			{
				Name:    "version",
				Aliases: []string{"--version", "-v"},
				Summary: "Print the CLI version",
				// Extra words are ignored rather than rejected, so `shed version`
				// stays usable however it is typed.
				MaxArgs: -1,
			},
			{
				Name:      "help",
				Aliases:   []string{"--help", "-h"},
				Usage:     "[command]",
				Summary:   "Show this help, or one command's options",
				MaxArgs:   1,
				UsageLine: "usage: shed help [command] [--output human|json]",
				Flags:     []Flag{outputFlag("human", humanJSON...)},
			},
		},
	},
}

// Environment documents the variables that change Shed's behavior.
//
// Only what someone using Shed would set. The control-plane and portal
// addresses are deliberately absent: they exist so this repository can be run
// against a local stack, and someone who installed a release has no reason to
// point the CLI somewhere else. Documenting them would offer a knob whose only
// working value is the one already in effect.
var Environment = []EnvVar{
	{Name: "SHED_TOKEN", Summary: "Authenticate with this token without storing it"},
	{Name: "NO_COLOR", Summary: "Disable color and bold in human output"},
}
