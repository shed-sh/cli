// Package execution coordinates a prepared, immutable bundle with a deployment
// backend. It deliberately knows nothing about project detection or source-tree
// inspection: callers must prepare and validate the bundle first.
package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"shed/internal/definition"
	"shed/internal/source"
)

type State string

const (
	StateAccepted         State = "accepted"
	StateBundleValidating State = "bundle_validating"
	StateBuildQueued      State = "build_queued"
	StateBuilding         State = "building"
	StateVerifying        State = "verifying"
	StateProvisioning     State = "provisioning"
	StateHealthChecking   State = "health_checking"
	StateReady            State = "ready"
	StateFailed           State = "failed"
	StateCancelling       State = "cancelling"
	StateCancelled        State = "cancelled"
)

func (state State) Terminal() bool {
	return state == StateReady || state == StateFailed || state == StateCancelled
}

func (state State) Valid() bool {
	switch state {
	case StateAccepted, StateBundleValidating, StateBuildQueued, StateBuilding,
		StateVerifying, StateProvisioning, StateHealthChecking, StateReady,
		StateFailed, StateCancelling, StateCancelled:
		return true
	default:
		return false
	}
}

type Stage string

const (
	StageAll     Stage = "all"
	StageSystem  Stage = "system"
	StageBuild   Stage = "build"
	StageRuntime Stage = "runtime"
)

func (stage Stage) Valid() bool {
	return stage == StageAll || stage == StageSystem || stage == StageBuild || stage == StageRuntime
}

type Request struct {
	ProjectName string              `json:"projectName"`
	RequestID   string              `json:"requestId"`
	Manifest    definition.Manifest `json:"manifest"`
	Archive     source.Archive      `json:"-"`
}

type Failure struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
	Operation   string `json:"operation,omitempty"`
}

type Links struct {
	Status string `json:"status,omitempty"`
	Logs   string `json:"logs,omitempty"`
	Cancel string `json:"cancel,omitempty"`
}

type Deployment struct {
	ID          string   `json:"id"`
	ProjectName string   `json:"projectName,omitempty"`
	State       State    `json:"state"`
	URL         string   `json:"url,omitempty"`
	Cursor      string   `json:"cursor,omitempty"`
	Failure     *Failure `json:"failure,omitempty"`
	Links       Links    `json:"links,omitempty"`
}

type Record struct {
	Cursor       string         `json:"cursor,omitempty"`
	Sequence     uint64         `json:"sequence,omitempty"`
	Kind         string         `json:"type"`
	Stage        Stage          `json:"stage"`
	Message      string         `json:"message,omitempty"`
	State        State          `json:"state,omitempty"`
	Timestamp    string         `json:"timestamp,omitempty"`
	DeploymentID string         `json:"deploymentId,omitempty"`
	Fields       map[string]any `json:"fields,omitempty"`
}

type Result struct {
	Type          string     `json:"type"`
	Outcome       string     `json:"outcome"`
	RequestID     string     `json:"requestId"`
	Project       Project    `json:"project"`
	Bundle        *Bundle    `json:"bundle,omitempty"`
	Deployment    Deployment `json:"deployment"`
	NextOperation string     `json:"nextOperation,omitempty"`
	// Definition says which definition file the deploy worked from and
	// whether it was written on the way. The CLI fills it in; the backend
	// never sees it.
	Definition *definition.Resolution `json:"definition,omitempty"`
}

type Project struct {
	Name string `json:"name"`
}

type Bundle struct {
	ContentDigest string `json:"contentDigest"`
	ArchiveDigest string `json:"archiveDigest"`
	FileCount     int    `json:"fileCount"`
	Size          int64  `json:"size"`
}

type Backend interface {
	Submit(context.Context, Request) (Deployment, error)
	Status(context.Context, string) (Deployment, error)
	Stream(context.Context, string, string, Stage, bool, func(Record) error) (string, error)
	Cancel(context.Context, string) (Deployment, error)
}

type WaitMode int

const (
	WaitDetach WaitMode = iota
	WaitTimeout
	WaitTerminal
)

type WaitOptions struct {
	Mode    WaitMode
	Timeout time.Duration
	Cursor  string
	Stage   Stage
}

type Observer func(Record) error

type Coordinator struct {
	Backend Backend
}

func (c Coordinator) Execute(ctx context.Context, request Request, wait WaitOptions, observer Observer) (Result, error) {
	if c.Backend == nil {
		return Result{}, errors.New("execution backend is required")
	}
	if request.ProjectName == "" || request.Archive.Path == "" {
		return Result{}, errors.New("project name and prepared archive are required")
	}
	if request.RequestID == "" {
		request.RequestID = DerivedRequestID(request)
	}
	deployment, err := c.Backend.Submit(ctx, request)
	if err != nil {
		return Result{}, fmt.Errorf("submit deployment: %w", err)
	}
	if err := validateDeployment(deployment); err != nil {
		return Result{}, err
	}
	result, err := c.finish(ctx, request.RequestID, deployment, wait, observer)
	result.Project.Name = request.ProjectName
	result.Bundle = &Bundle{
		ContentDigest: request.Archive.Content.Digest,
		ArchiveDigest: request.Archive.Digest,
		FileCount:     request.Archive.Content.FileCount,
		Size:          request.Archive.CompressedSize,
	}
	return result, err
}

func (c Coordinator) Resume(ctx context.Context, deploymentID string, wait WaitOptions, observer Observer) (Result, error) {
	if c.Backend == nil {
		return Result{}, errors.New("execution backend is required")
	}
	deployment, err := c.Backend.Status(ctx, deploymentID)
	if err != nil {
		return Result{}, fmt.Errorf("get deployment status: %w", err)
	}
	if err := validateDeployment(deployment); err != nil {
		return Result{}, err
	}
	return c.finish(ctx, "", deployment, wait, observer)
}

func (c Coordinator) finish(ctx context.Context, requestID string, deployment Deployment, wait WaitOptions, observer Observer) (Result, error) {
	if wait.Stage == "" {
		wait.Stage = StageAll
	}
	if !wait.Stage.Valid() {
		return Result{}, fmt.Errorf("invalid log stage %q", wait.Stage)
	}
	streamContext := ctx
	cancel := func() {}
	if wait.Mode == WaitTimeout {
		timeout := wait.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		streamContext, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	for wait.Mode != WaitDetach && !deployment.State.Terminal() {
		cursor, streamErr := c.Backend.Stream(streamContext, deployment.ID, deployment.Cursor, wait.Stage, true, func(record Record) error {
			if record.Cursor != "" {
				deployment.Cursor = record.Cursor
			}
			if record.State != "" {
				if !record.State.Valid() {
					return fmt.Errorf("deployment stream returned unknown state %q", record.State)
				}
				deployment.State = record.State
			}
			if observer != nil {
				return observer(record)
			}
			return nil
		})
		if cursor != "" {
			deployment.Cursor = cursor
		}
		if streamErr != nil && !errors.Is(streamErr, context.DeadlineExceeded) && !errors.Is(streamErr, context.Canceled) {
			return Result{}, fmt.Errorf("follow deployment: %w", streamErr)
		}
		// The snapshot is authoritative after a stream closes or a finite wait
		// expires. It also recovers a terminal event lost during disconnect.
		current, statusErr := c.Backend.Status(ctx, deployment.ID)
		if statusErr != nil {
			if streamErr != nil && wait.Mode == WaitTimeout {
				return resultFor(requestID, deployment), nil
			}
			return Result{}, fmt.Errorf("reconcile deployment status: %w", statusErr)
		}
		if current.Cursor == "" {
			current.Cursor = deployment.Cursor
		}
		if err := validateDeployment(current); err != nil {
			return Result{}, err
		}
		deployment = current
		if wait.Mode == WaitTimeout && streamContext.Err() != nil {
			break
		}
		if wait.Mode == WaitTerminal && !deployment.State.Terminal() {
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	return resultFor(requestID, deployment), nil
}

func validateDeployment(deployment Deployment) error {
	if deployment.ID == "" {
		return errors.New("deployment response has no identifier")
	}
	if !deployment.State.Valid() {
		return fmt.Errorf("deployment %s returned unknown state %q", deployment.ID, deployment.State)
	}
	if deployment.State == StateReady && deployment.URL == "" {
		return fmt.Errorf("deployment %s is ready but has no URL", deployment.ID)
	}
	return nil
}

func resultFor(requestID string, deployment Deployment) Result {
	if deployment.State != StateReady {
		deployment.URL = ""
	}
	outcome := "pending"
	next := "status"
	switch deployment.State {
	case StateReady:
		outcome, next = "ready", ""
	case StateFailed:
		outcome, next = "failed", "logs"
	case StateCancelled:
		outcome, next = "cancelled", ""
	}
	return Result{Type: "result", Outcome: outcome, RequestID: requestID, Project: Project{Name: deployment.ProjectName}, Deployment: deployment, NextOperation: next}
}

func DerivedRequestID(request Request) string {
	fingerprint := struct {
		ProjectName   string              `json:"projectName"`
		ContentDigest string              `json:"contentDigest"`
		ArchiveDigest string              `json:"archiveDigest"`
		Manifest      definition.Manifest `json:"manifest"`
	}{request.ProjectName, request.Archive.Content.Digest, request.Archive.Digest, request.Manifest}
	data, _ := json.Marshal(fingerprint)
	digest := sha256.Sum256(data)
	return "req_" + hex.EncodeToString(digest[:12])
}
