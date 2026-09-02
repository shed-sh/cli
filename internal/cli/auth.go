package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	"shed/internal/api"
	"shed/internal/auth"
	"shed/internal/clispec"
	"shed/internal/config"
	"shed/internal/credentials"
	"shed/internal/diag"
)

func (a *App) login(ctx context.Context, b *clispec.Binding) error {
	if err := ctx.Err(); err != nil {
		return cancelledLogin(a.stderr, err)
	}
	tokenName := b.String("name")
	if tokenName == "" {
		tokenName = auth.DefaultTokenName()
	} else if err := auth.ValidateTokenName(tokenName); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Token resolution ladder: --token > SHED_TOKEN env > piped stdin > interactive.
	// The last step needs a real terminal; if we get there without one, fail
	// with a stable diagnostic instead of hanging on a hidden stdin read.
	if token := strings.TrimSpace(b.String("token")); token != "" {
		return a.saveDirectToken(ctx, cfg, token, "flag")
	}
	if env := strings.TrimSpace(os.Getenv("SHED_TOKEN")); env != "" {
		return a.saveDirectToken(ctx, cfg, env, "env")
	}
	if !a.stdinIsTerminal() {
		if piped, ok := a.readPipedToken(); ok {
			return a.saveDirectToken(ctx, cfg, piped, "stdin")
		}
		return &diag.Error{
			Code:    "login_requires_terminal",
			Summary: "shed login is interactive; there is no terminal here to prompt on.",
			Hints: []string{
				"Pass a personal access token: shed login --token <pat>",
				"Or export it: SHED_TOKEN=<pat>",
				"Or pipe it in: echo <pat> | shed login",
			},
		}
	}

	if mismatch := mismatchedLoginEndpoints(cfg); mismatch != nil {
		return mismatch
	}

	// Loopback delivery only makes sense when we are the ones opening the
	// browser: it relies on the browser and this process sharing a machine.
	// With --no-browser the link is carried to some other device, so the code
	// has to come back the way it always has.
	var receiver *loopbackReceiver
	var redirect auth.Redirect
	if !b.Bool("no-browser") {
		started, err := startLoopback()
		if err != nil {
			// Not fatal. A sandbox that refuses the bind still logs in fine by
			// typing the code, so this is a downgrade and not a failure.
			_, _ = fmt.Fprintf(a.stderr, "warning: could not listen for the browser callback: %v\n", err)
		} else {
			receiver = started
			defer receiver.Close()
			redirect = auth.Redirect{URI: receiver.RedirectURI(), State: receiver.State()}
		}
	}

	// Nothing here reaches the network. The link is printed and the browser
	// opens even when the control plane is unreachable, and the failure -- if
	// there is going to be one -- surfaces after someone has approved.
	attempt, err := auth.BeginLogin(api.New(cfg.APIURL, ""), tokenName, cfg.PortalURL, redirect)
	if err != nil {
		return unusablePortalURL(cfg.PortalURL, err)
	}

	// The authorization URL is printed only when someone actually has to carry
	// it somewhere: --no-browser, or a browser that failed to open. When the
	// browser opens, the link on screen is scrollback noise wrapped around a
	// session key nobody should need to see.
	switch {
	case b.Bool("no-browser"):
		a.printLoginPrompt(attempt.Session.AuthorizationURL)
	default:
		if err := a.openBrowser(attempt.Session.AuthorizationURL); err != nil {
			_, _ = fmt.Fprintf(a.stderr, "warning: could not open a browser: %v\n", err)
			a.printLoginPrompt(attempt.Session.AuthorizationURL)
		} else {
			_, _ = fmt.Fprintln(a.stdout, "Opened your browser to sign in.")
			_, _ = fmt.Fprintln(a.stdout, "If no page appeared, rerun with --no-browser to get a link instead.")
		}
	}
	result, err := a.completeLogin(ctx, attempt, receiver)
	if err != nil {
		return cancelledLogin(a.stderr, err)
	}
	saveResult, err := a.credentials.Save(cfg.APIURL, result.Token)
	if err != nil {
		saveErr := fmt.Errorf("save login credentials: %w", err)
		if revokeErr := api.New(cfg.APIURL, result.Token).RevokeCurrentToken(ctx); revokeErr != nil {
			return errors.Join(saveErr, fmt.Errorf("revoke unstored token: %w", revokeErr))
		}
		return saveErr
	}
	if saveResult.UsedFileFallback {
		_, _ = fmt.Fprintln(a.stderr, "warning: system keyring unavailable; saved credentials in the owner-only config file")
	}

	// Which workspace this token speaks for. Asked rather than taken from the
	// exchange, which does not carry it, and asked through the same call
	// `whoami` uses so the two can never say different things. Best-effort:
	// the credential is already saved and working, so a failure here is a
	// missing line, not a failed login.
	workspace := ""
	if current, err := api.New(cfg.APIURL, result.Token).CurrentUser(ctx); err == nil {
		workspace = current.Tenant.Slug
	}

	switch {
	case result.User.Email != "" && workspace != "":
		_, _ = fmt.Fprintf(a.stdout, "Logged in as %s in workspace %s.\n", result.User.Email, workspace)
	case result.User.Email != "":
		_, _ = fmt.Fprintf(a.stdout, "Logged in as %s.\n", result.User.Email)
	case result.User.ID != "":
		_, _ = fmt.Fprintf(a.stdout, "Logged in as %s.\n", result.User.ID)
	default:
		_, _ = fmt.Fprintln(a.stdout, "Logged in.")
	}
	return nil
}

// maxVerificationAttempts bounds how many times a mistyped code may be retried
// before the CLI gives up on the session.
const maxVerificationAttempts = 3

// completeLogin prompts for the verification code, re-prompting when the server
// says the code itself was wrong. Any other failure — expired session, consumed
// authorization, rate limit, transport error — ends the attempt immediately,
// because retyping the code cannot fix it.
func (a *App) completeLogin(
	ctx context.Context,
	attempt *auth.LoginAttempt,
	receiver *loopbackReceiver,
) (auth.LoginResult, error) {
	if receiver != nil {
		result, handled, err := a.awaitCallback(ctx, attempt, receiver)
		if handled {
			return result, err
		}
		// The browser did not deliver, so fall through to typing the code.
		// Nothing is lost: the session is the same one the approval page is
		// looking at, and its code is still valid.
	}
	return a.promptForCode(ctx, attempt)
}

// awaitCallback waits for the browser to hand the code over, while leaving the
// keyboard live as an escape hatch.
//
// The escape hatch matters because loopback can fail in ways this process
// cannot see: the browser opened on a different machine, a proxy swallowed the
// redirect, the person approved on their phone. Rather than hang until the
// session expires, any keypress drops back to typing the code.
//
// handled is false when the caller should take that fallback.
func (a *App) awaitCallback(
	ctx context.Context,
	attempt *auth.LoginAttempt,
	receiver *loopbackReceiver,
) (auth.LoginResult, bool, error) {
	_, _ = fmt.Fprintln(a.stdout, "Waiting for you to approve the login in your browser.")
	_, _ = fmt.Fprintln(a.stdout, "Press Enter to type the verification code instead.")

	// Cancelling this is what unblocks the pending stdin read once the callback
	// wins, so the keyboard watcher does not outlive the thing it was for.
	watchCtx, stopWatching := context.WithCancel(ctx)
	defer stopWatching()

	typed := make(chan string, 1)
	go func() {
		line, err := a.readLine(watchCtx)
		if err != nil {
			return
		}
		typed <- line
	}()

	select {
	case <-ctx.Done():
		return auth.LoginResult{}, true, ctx.Err()

	case code := <-receiver.Codes():
		stopWatching()
		result, err := attempt.Complete(ctx, code)
		if err != nil {
			// The browser delivered a code the server then refused. Retyping
			// it cannot help: it is the code the server itself just issued.
			return auth.LoginResult{}, true, err
		}
		return result, true, nil

	case line := <-typed:
		// A line that already looks like the code should not cost an extra
		// prompt; an empty line is just the request to be asked.
		if line == "" {
			return auth.LoginResult{}, false, nil
		}
		result, err := attempt.Complete(ctx, line)
		if err == nil {
			return result, true, nil
		}
		if !retryableVerificationCode(line, err) {
			return auth.LoginResult{}, true, err
		}
		_, _ = fmt.Fprintln(a.stderr, "That code was not accepted.")
		return auth.LoginResult{}, false, nil
	}
}

func (a *App) promptForCode(ctx context.Context, attempt *auth.LoginAttempt) (auth.LoginResult, error) {
	for remaining := maxVerificationAttempts; ; remaining-- {
		if err := ctx.Err(); err != nil {
			return auth.LoginResult{}, err
		}
		_, _ = fmt.Fprint(a.stdout, "After signing in, enter the verification code: ")
		code, err := a.readLine(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return auth.LoginResult{}, err
			}
			return auth.LoginResult{}, fmt.Errorf("read verification code: %w", err)
		}

		result, err := attempt.Complete(ctx, code)
		if err == nil {
			return result, nil
		}
		if remaining <= 1 || !retryableVerificationCode(code, err) {
			return auth.LoginResult{}, err
		}
		if code == "" {
			_, _ = fmt.Fprintln(a.stderr, "A verification code is required.")
		} else {
			_, _ = fmt.Fprintf(a.stderr, "That code was not accepted; %d attempts left.\n", remaining-1)
		}
	}
}

// retryableVerificationCode reports whether re-prompting could plausibly succeed.
// An empty line never reached the server, and the server flags a mistyped code
// explicitly; everything else is treated as terminal.
func retryableVerificationCode(code string, err error) bool {
	if code == "" {
		return true
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == api.ErrCodeInvalidVerificationCode
}

func (a *App) logout(ctx context.Context, b *clispec.Binding) error {
	localOnly := b.Bool("local")

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	credential, err := a.credentials.Resolve(cfg.APIURL)
	if errors.Is(err, credentials.ErrNotFound) {
		_, _ = fmt.Fprintln(a.stdout, "Not logged in.")
		return nil
	}
	if err != nil {
		return err
	}
	if credential.Source == credentials.SourceEnvironment && localOnly {
		return fmt.Errorf("environment variable %s is set; unset it to remove the active credential", "SHED_TOKEN")
	}

	if !localOnly {
		if err := api.New(cfg.APIURL, credential.Token).RevokeCurrentToken(ctx); err != nil {
			return err
		}
	}
	if credential.Source == credentials.SourceEnvironment {
		_, _ = fmt.Fprintln(a.stdout, "Token revoked. Unset SHED_TOKEN to remove it from this shell.")
		return nil
	}
	if err := a.credentials.Delete(cfg.APIURL, credential.Source); err != nil {
		return fmt.Errorf("remove local credentials: %w", err)
	}
	_, _ = fmt.Fprintln(a.stdout, "Logged out.")
	return nil
}

func (a *App) whoami(ctx context.Context, _ *clispec.Binding) error {
	client, err := a.configuredClient()
	if err != nil {
		return err
	}
	user, err := client.CurrentUser(ctx)
	if err != nil {
		return err
	}
	if user.Email != "" {
		_, _ = fmt.Fprintln(a.stdout, user.Email)
	}
	if user.Tenant.Slug != "" {
		// Above the user id, because it is the answer to the question people
		// actually ask this command: not "who am I" but "where will this
		// deploy". A token names one workspace, and nothing else on screen
		// says which.
		_, _ = fmt.Fprintf(a.stdout, "Workspace: %s\n", user.Tenant.Slug)
	}
	if user.ID != "" {
		_, _ = fmt.Fprintf(a.stdout, "User ID: %s\n", user.ID)
	}
	if user.Email == "" && user.ID == "" {
		return errors.New("shed API returned an empty user identity")
	}
	return nil
}

func (a *App) configuredClient() (*api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	credential, err := a.credentials.Resolve(cfg.APIURL)
	if errors.Is(err, credentials.ErrNotFound) {
		return nil, api.ErrNotLoggedIn
	}
	if err != nil {
		return nil, err
	}
	return api.New(cfg.APIURL, credential.Token), nil
}

// maxPromptInput caps everything the CLI will ever read from stdin across all
// prompts. Prompts only ever collect short codes, so the budget exists purely to
// keep a runaway pipe from being read into memory.
const maxPromptInput = 64 * 1024

// readLine reads one line from stdin. The buffered reader is created once and
// reused: a fresh bufio.Reader per call would discard whatever it buffered past
// the first newline, silently eating later prompts' input.
//
// The blocking read runs on a goroutine so ctx cancellation (Ctrl+C) unblocks
// the caller instead of hanging on stdin. Once cancelled the goroutine may
// linger — process exit reclaims it, and login does not call readLine again
// after a cancellation.
func (a *App) readLine(ctx context.Context) (string, error) {
	if a.stdinReader == nil {
		a.stdinReader = bufio.NewReader(io.LimitReader(a.stdin, maxPromptInput))
	}
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := a.stdinReader.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		if r.err != nil && !errors.Is(r.err, io.EOF) {
			return "", r.err
		}
		if r.line == "" {
			return "", errors.New("input closed")
		}
		return strings.TrimSpace(r.line), nil
	}
}

// cancelledLogin turns a context cancellation into a friendly one-line notice
// on stderr plus the already-reported sentinel, so Ctrl+C during login exits
// non-zero without the generic "error: context canceled" wrapper.
// mismatchedLoginEndpoints refuses a login ceremony split across two stacks.
// The browser approves the session on the portal's control plane while this
// process collects the token from the API URL; when exactly one of the pair
// points away from hosted Shed, the two sides talk past each other and the
// ceremony can never complete. Refusing here, before any browser opens, beats
// a connection error after someone has already approved a login whose token
// was never going to arrive.
func mismatchedLoginEndpoints(cfg config.Config) *diag.Error {
	apiIsDefault := sameEndpoint(cfg.APIURL, config.DefaultAPIURL)
	portalIsDefault := sameEndpoint(cfg.PortalURL, config.DefaultPortalURL)
	if apiIsDefault == portalIsDefault {
		return nil
	}
	configFile := "the Shed configuration file"
	if path, err := config.Path(); err == nil {
		configFile = path
	}
	return &diag.Error{
		Code:    "mismatched_login_endpoints",
		Summary: "shed login cannot mix a custom address with a built-in default.",
		Facts: []diag.Fact{
			{Label: "API URL", Value: describeEndpoint(cfg.APIURL, apiIsDefault, config.APIURLEnv, configFile)},
			{Label: "Portal URL", Value: describeEndpoint(cfg.PortalURL, portalIsDefault, config.PortalURLEnv, configFile)},
		},
		Hints: []string{
			"The browser approves the login on the portal's control plane, and the CLI collects the token from the API; a mixed pair never completes.",
			"For a local or private stack, set both: " + config.APIURLEnv + "=https://<api> " + config.PortalURLEnv + "=https://<portal> shed login",
			"For hosted Shed, remove the custom address from the environment or " + configFile + ".",
		},
	}
}

func sameEndpoint(left, right string) bool {
	return strings.TrimRight(left, "/") == strings.TrimRight(right, "/")
}

func describeEndpoint(value string, isDefault bool, envName, configFile string) string {
	switch {
	case isDefault:
		return value + " (built-in default)"
	case os.Getenv(envName) != "":
		return value + " (from " + envName + ")"
	default:
		return value + " (from " + configFile + ")"
	}
}

// unusablePortalURL reports a portal address the CLI cannot build a link from.
//
// This is configuration, not a network problem, so it names the setting and
// where it came from rather than suggesting the control plane is down.
func unusablePortalURL(portalURL string, cause error) error {
	source := "built-in default"
	if os.Getenv(config.PortalURLEnv) != "" {
		source = "from " + config.PortalURLEnv
	} else if portalURL != config.DefaultPortalURL {
		if path, pathErr := config.Path(); pathErr == nil {
			source = "from " + path
		}
	}
	return &diag.Error{
		Code:    "portal_url_invalid",
		Summary: "The Shed portal address is not a usable URL.",
		Facts: []diag.Fact{
			{Label: "Portal URL", Value: portalURL + " (" + source + ")"},
			{Label: "Cause", Value: cause.Error()},
		},
		Hints: []string{
			"shed login opens this address in a browser, so it must be absolute, for example " + config.DefaultPortalURL,
			"Remove the portalUrl setting to fall back to " + config.DefaultPortalURL + ".",
		},
		Cause: cause,
	}
}

func cancelledLogin(stderr io.Writer, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		_, _ = fmt.Fprintln(stderr, "Login cancelled.")
		return errAlreadyReported
	}
	return err
}

// stdinIsTerminal reports whether the stdin the App is bound to is a real
// terminal. A pipe (echo <token> | shed login) is a *os.File whose fd is not a
// tty, so the ladder correctly branches into readPipedToken. Test doubles like
// strings.Reader and io.Pipe are not *os.File — those are synthetic scripted
// inputs, and treating them as interactive lets the existing interactive-flow
// tests keep exercising the verification-code prompt without a hook.
func (a *App) stdinIsTerminal() bool {
	file, ok := a.stdin.(*os.File)
	if !ok {
		return true
	}
	return term.IsTerminal(file.Fd())
}

// readPipedToken reads a single non-empty line from stdin. It is called only
// when stdin is not a terminal — a pipe — so the read cannot hang on a real
// user's keyboard. Returns false if no token was piped in.
func (a *App) readPipedToken() (string, bool) {
	reader := bufio.NewReader(io.LimitReader(a.stdin, maxPromptInput))
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false
	}
	token := strings.TrimSpace(line)
	return token, token != ""
}

// saveDirectToken persists a token supplied through the non-interactive ladder
// (flag / env / stdin), after verifying it against the backend. The backend
// call also fills in the user identity we echo back, so a bad token fails
// loudly here rather than at the next command.
func (a *App) saveDirectToken(ctx context.Context, cfg config.Config, token, source string) error {
	client := api.New(cfg.APIURL, token)
	user, err := client.CurrentUser(ctx)
	if err != nil {
		return fmt.Errorf("verify token: %w", err)
	}
	saveResult, err := a.credentials.Save(cfg.APIURL, token)
	if err != nil {
		return fmt.Errorf("save login credentials: %w", err)
	}
	if saveResult.UsedFileFallback {
		_, _ = fmt.Fprintln(a.stderr, "warning: system keyring unavailable; saved credentials in the owner-only config file")
	}
	switch {
	case user.Email != "":
		_, _ = fmt.Fprintf(a.stdout, "Logged in as %s (via %s).\n", user.Email, source)
	case user.ID != "":
		_, _ = fmt.Fprintf(a.stdout, "Logged in as %s (via %s).\n", user.ID, source)
	default:
		_, _ = fmt.Fprintf(a.stdout, "Logged in (via %s).\n", source)
	}
	return nil
}

// printLoginPrompt renders the "open this link" preamble. On a real terminal
// it uses OSC 8 to make the URL itself clickable in supporting terminals
// (iTerm2, Kitty, WezTerm, Ghostty, VS Code, Windows Terminal), and wraps the
// block in a subtle border via lipgloss. Elsewhere it degrades to plain text.
func (a *App) printLoginPrompt(url string) {
	styler := diag.NewStyler(a.stdout)
	displayURL := url
	if stderrIsTerminal(a.stderr) {
		// OSC 8 hyperlink: ESC ] 8 ; ; <url> ST <label> ESC ] 8 ; ; ST.
		// Terminals that don't grok it render just the label — which is the
		// URL — so nothing is lost on older emulators.
		displayURL = fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", url, url)
	}
	_, _ = fmt.Fprintln(a.stdout, styler.Strong("Open this link to sign in:"))
	_, _ = fmt.Fprintln(a.stdout, "  "+displayURL)
	_, _ = fmt.Fprintln(a.stdout)
}

func stderrIsTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(file.Fd())
}
