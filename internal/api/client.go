package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"shed/internal/config"
	"shed/internal/diag"
	"shed/internal/execution"
)

const eventsPageLimit = 200

type Client struct {
	baseURL      string
	token        string
	httpClient   *http.Client
	pollInterval time.Duration
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		pollInterval: time.Second,
	}
}

// NewWithHTTPClient allows callers and protocol tests to control transport
// behavior without changing the production defaults.
func NewWithHTTPClient(baseURL, token string, httpClient *http.Client) *Client {
	client := New(baseURL, token)
	if httpClient != nil {
		client.httpClient = httpClient
	}
	return client
}

type bundleResource struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Reused      bool   `json:"reused"`
	FailureCode string `json:"failureCode"`
}

// Submit implements execution.Backend: walk the bundle to ready, then create
// the deployment under the caller's idempotency key.
func (c *Client) Submit(ctx context.Context, request execution.Request) (execution.Deployment, error) {
	bundle, err := c.ensureBundle(ctx, request)
	if err != nil {
		return execution.Deployment{}, err
	}
	var deployment execution.Deployment
	body := map[string]any{
		"project":     request.ProjectName,
		"projectKind": request.Manifest.WorkloadKind(),
		"bundleId":    bundle.ID,
		"runtime":     request.Manifest.Run,
	}
	if err := c.requestJSONHeaders(ctx, http.MethodPost, "/v1/cli/deployments", body, &deployment, true, map[string]string{
		"Idempotency-Key": request.RequestID,
	}); err != nil {
		return execution.Deployment{}, err
	}
	return deployment, nil
}

// ensureBundle registers the bundle (content-addressed: a reused ready bundle
// skips every later step), uploads the archive through the API while the
// bundle is still pending, finalizes with the declared digest, and polls
// until validation settles.
func (c *Client) ensureBundle(ctx context.Context, request execution.Request) (bundleResource, error) {
	var bundle bundleResource
	if err := c.requestJSON(ctx, http.MethodPost, "/v1/cli/bundles", map[string]any{
		"project":       request.ProjectName,
		"projectKind":   request.Manifest.WorkloadKind(),
		"kind":          request.Manifest.BundleKind(),
		"contentDigest": request.Archive.Content.Digest,
	}, &bundle, true); err != nil {
		return bundleResource{}, err
	}
	if bundle.ID == "" {
		return bundleResource{}, errors.New("bundle registration returned no identifier")
	}
	if bundle.Status == "pending" {
		if err := c.UploadSource(ctx, bundle.ID, request.Archive.Path); err != nil {
			return bundleResource{}, err
		}
		if err := c.requestJSON(ctx, http.MethodPost, "/v1/cli/bundles/"+url.PathEscape(bundle.ID)+"/finalize", map[string]string{
			"archiveDigest": request.Archive.Digest,
		}, &bundle, true); err != nil {
			return bundleResource{}, err
		}
	}
	for {
		switch bundle.Status {
		case "ready":
			return bundle, nil
		case "failed":
			code := bundle.FailureCode
			if code == "" {
				code = "bundle_failed"
			}
			return bundleResource{}, fmt.Errorf("bundle validation failed: %s", code)
		}
		select {
		case <-ctx.Done():
			return bundleResource{}, ctx.Err()
		case <-time.After(c.pollInterval):
		}
		if err := c.requestJSON(ctx, http.MethodGet, "/v1/cli/bundles/"+url.PathEscape(bundle.ID), nil, &bundle, true); err != nil {
			return bundleResource{}, err
		}
	}
}

func (c *Client) Status(ctx context.Context, deploymentID string) (execution.Deployment, error) {
	var deployment execution.Deployment
	err := c.requestJSON(ctx, http.MethodGet, "/v1/cli/deployments/"+url.PathEscape(deploymentID), nil, &deployment, true)
	return deployment, err
}

// Stream implements execution.Backend over the ordered deployment event
// trail: Bench exposes polling with an `after` sequence cursor, not a push
// stream. With follow, polling continues until a terminal state event
// arrives or the context ends; the coordinator's status snapshot after the
// stream stays authoritative either way.
func (c *Client) Stream(ctx context.Context, deploymentID, cursor string, stage execution.Stage, follow bool, onRecord func(execution.Record) error) (string, error) {
	after := int64(0)
	if cursor != "" {
		parsed, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return cursor, fmt.Errorf("invalid event cursor %q", cursor)
		}
		after = parsed
	}
	for {
		events, err := c.deploymentEvents(ctx, deploymentID, after)
		if err != nil {
			return cursorFor(after), err
		}
		terminal := false
		for _, event := range events {
			after = event.Sequence
			record := recordFor(deploymentID, event)
			if record.State.Terminal() {
				terminal = true
			}
			if stage != "" && stage != execution.StageAll && record.Stage != stage {
				continue
			}
			if onRecord != nil {
				if err := onRecord(record); err != nil {
					return cursorFor(after), err
				}
			}
		}
		if terminal || (!follow && len(events) < eventsPageLimit) {
			return cursorFor(after), nil
		}
		if len(events) == eventsPageLimit {
			continue // full page: drain the next one immediately
		}
		select {
		case <-ctx.Done():
			return cursorFor(after), ctx.Err()
		case <-time.After(c.pollInterval):
		}
	}
}

type deploymentEvent struct {
	Sequence  int64  `json:"sequence"`
	Type      string `json:"type"`
	FromState string `json:"fromState"`
	ToState   string `json:"toState"`
	CreatedAt string `json:"createdAt"`
}

func (c *Client) deploymentEvents(ctx context.Context, deploymentID string, after int64) ([]deploymentEvent, error) {
	var payload struct {
		Events []deploymentEvent `json:"events"`
	}
	path := fmt.Sprintf("/v1/cli/deployments/%s/events?after=%d&limit=%d",
		url.PathEscape(deploymentID), after, eventsPageLimit)
	if err := c.requestJSON(ctx, http.MethodGet, path, nil, &payload, true); err != nil {
		return nil, err
	}
	return payload.Events, nil
}

func recordFor(deploymentID string, event deploymentEvent) execution.Record {
	record := execution.Record{
		Cursor:       strconv.FormatInt(event.Sequence, 10),
		Sequence:     uint64(event.Sequence),
		Kind:         event.Type,
		Stage:        stageFor(event.Type),
		State:        execution.State(event.ToState),
		Timestamp:    event.CreatedAt,
		DeploymentID: deploymentID,
		Message:      event.Type,
	}
	if event.FromState != "" || event.ToState != "" {
		record.Message = event.FromState + " -> " + event.ToState
	}
	return record
}

func stageFor(eventType string) execution.Stage {
	switch {
	case strings.HasPrefix(eventType, "build."):
		return execution.StageBuild
	case strings.HasPrefix(eventType, "runtime."):
		return execution.StageRuntime
	default:
		return execution.StageSystem
	}
}

func cursorFor(after int64) string {
	if after == 0 {
		return ""
	}
	return strconv.FormatInt(after, 10)
}

func (c *Client) Cancel(ctx context.Context, deploymentID string) (execution.Deployment, error) {
	var deployment execution.Deployment
	err := c.requestJSON(ctx, http.MethodPost, "/v1/cli/deployments/"+url.PathEscape(deploymentID)+"/cancel", struct{}{}, &deployment, true)
	return deployment, err
}

// UploadSource streams the archive through the authenticated API; there is
// no signed-URL indirection.
func (c *Client) UploadSource(ctx context.Context, bundleID, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open source bundle: %w", err)
	}
	defer func() { _ = file.Close() }()

	request, err := c.newRequest(ctx, http.MethodPut, "/v1/cli/bundles/"+url.PathEscape(bundleID)+"/archive", file, true)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/gzip")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("upload source: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return responseError(response)
	}
	return nil
}

func (c *Client) Share(ctx context.Context, deploymentID, email string) error {
	return c.requestJSON(ctx, http.MethodPost, "/v1/cli/deployments/"+url.PathEscape(deploymentID)+"/grants", map[string]string{
		"email": email,
	}, nil, true)
}

func (c *Client) Revoke(ctx context.Context, deploymentID, email string) error {
	path := "/v1/cli/deployments/" + url.PathEscape(deploymentID) + "/grants/" + url.PathEscape(email)
	return c.requestJSON(ctx, http.MethodDelete, path, nil, nil, true)
}

func (c *Client) requestJSON(ctx context.Context, method, path string, body, result any, authenticated bool) error {
	return c.requestJSONHeaders(ctx, method, path, body, result, authenticated, nil)
}

func (c *Client) requestJSONHeaders(ctx context.Context, method, path string, body, result any, authenticated bool, headers map[string]string) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	request, err := c.newRequest(ctx, method, path, reader, authenticated)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return c.transportError(request, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return responseError(response)
	}
	if result == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("decode Shed API response: %w", err)
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader, authenticated bool) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create Shed API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "shed")
	if authenticated {
		if c.token == "" {
			return nil, ErrNotLoggedIn
		}
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	return request, nil
}

// Error is a non-2xx response from the Shed API. Code carries the machine-readable
// "error" field when the server sends one, so callers can branch on the failure
// without matching on human-facing text.
type Error struct {
	StatusCode int
	Status     string
	Code       string
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("Shed API returned %s: %s", e.Status, e.Message)
}

func responseError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	message := strings.TrimSpace(string(data))
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &payload) == nil {
		if payload.Message != "" {
			message = payload.Message
		} else if payload.Error != "" {
			message = payload.Error
		}
	}
	if message == "" {
		message = response.Status
	}
	return &Error{
		StatusCode: response.StatusCode,
		Status:     response.Status,
		Code:       payload.Error,
		Message:    message,
	}
}

// transportError turns a failed round trip into guidance.
//
// A bare transport error names a socket and nothing else: it does not say what
// the CLI was reaching for, that the address came from a default rather than a
// choice, or which knob moves it. That leaves whoever reads it — person or
// agent — with a symptom and no next step, so the failure is restated here in
// terms of intent, target, and remedy.
func (c *Client) transportError(request *http.Request, err error) error {
	// Cancellation is the caller's own doing. Dressing it up as a
	// reachability problem would send people chasing a network that is fine.
	if errors.Is(err, context.Canceled) {
		return err
	}

	failure := &diag.Error{
		Code:    "api_unreachable",
		Summary: fmt.Sprintf("Could not reach the Shed API at %s.", c.baseURL),
		Cause:   err,
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		failure.Code = "api_timeout"
		failure.Summary = fmt.Sprintf("The Shed API at %s did not respond in time.", c.baseURL)
	}
	failure.Facts = []diag.Fact{
		{Label: "Tried", Value: request.Method + " " + request.URL.String()},
		{Label: "Cause", Value: transportCause(err)},
		{Label: "API URL", Value: describeAPIURL(c.baseURL)},
	}
	failure.Hints = c.reachabilityHints()
	return failure
}

// transportCause strips the url.Error envelope, whose method-and-URL prefix the
// Tried fact already carries, down to what actually went wrong.
func transportCause(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	return err.Error()
}

// The three places a control-plane address can come from. They are told apart
// because the remedy differs: a default wants a local control plane or an
// override, an override wants checking, and a file wants editing.
const (
	sourceDefault = iota
	sourceEnv
	sourceFile
)

func apiURLSource(baseURL string) int {
	switch {
	case os.Getenv(config.APIURLEnv) != "":
		return sourceEnv
	case strings.TrimRight(baseURL, "/") == strings.TrimRight(config.DefaultAPIURL, "/"):
		return sourceDefault
	default:
		return sourceFile
	}
}

// describeAPIURL credits the address to whatever set it. "Connection refused"
// on an address nobody chose reads very differently from one someone set, and
// naming the file beats leaving a reader to guess between three sources.
func describeAPIURL(baseURL string) string {
	switch apiURLSource(baseURL) {
	case sourceEnv:
		return baseURL + " (from " + config.APIURLEnv + ")"
	case sourceFile:
		if path, err := config.Path(); err == nil {
			return baseURL + " (from " + path + ")"
		}
		return baseURL + " (from the Shed configuration file)"
	default:
		return baseURL + " (built-in default)"
	}
}

func (c *Client) reachabilityHints() []string {
	switch apiURLSource(c.baseURL) {
	case sourceEnv:
		return []string{
			config.APIURLEnv + " points the CLI at " + c.baseURL + "; check that it is reachable from this machine.",
			"Unset it to fall back to the default, " + config.DefaultAPIURL + ".",
		}
	case sourceFile:
		hint := "Change the address: export " + config.APIURLEnv + "=https://<host>"
		if path, err := config.Path(); err == nil {
			hint = "Change the address in " + path + ", or override it: export " + config.APIURLEnv + "=https://<host>"
		}
		// A loopback address explains its own reachability: the control plane
		// is either running here or it is not, so asking whether the network
		// can get there adds nothing.
		if isLoopback(c.baseURL) {
			return []string{
				"This address is on this machine, so it needs a Shed control plane running locally.",
				hint,
			}
		}
		return []string{"Check that " + c.baseURL + " is reachable from this machine.", hint}
	default:
		// Nobody chose this address, so nobody can be asked to correct it.
		// Naming the override here would hand someone a setting that exists
		// for developing Shed and invite them to configure their way out of
		// what is either their connection or an outage.
		return []string{
			"Shed did not answer. Check your network connection and try again.",
			"If it keeps failing, Shed may be temporarily unavailable.",
		}
	}
}

func isLoopback(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
