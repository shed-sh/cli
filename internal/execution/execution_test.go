package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"shed/internal/definition"
	"shed/internal/source"
)

type fakeBackend struct {
	submitted Request
	current   Deployment
	records   []Record
	block     bool
}

func (backend *fakeBackend) Submit(_ context.Context, request Request) (Deployment, error) {
	backend.submitted = request
	return backend.current, nil
}

func (backend *fakeBackend) Status(context.Context, string) (Deployment, error) {
	return backend.current, nil
}

func (backend *fakeBackend) Stream(ctx context.Context, _ string, cursor string, _ Stage, _ bool, observer func(Record) error) (string, error) {
	for _, record := range backend.records {
		if err := observer(record); err != nil {
			return cursor, err
		}
		cursor = record.Cursor
		if record.State.Terminal() {
			backend.current.State = record.State
			backend.current.URL = "https://example.shed.run"
		}
	}
	backend.records = nil
	if backend.block {
		<-ctx.Done()
		return cursor, ctx.Err()
	}
	return cursor, nil
}

func (backend *fakeBackend) Cancel(context.Context, string) (Deployment, error) {
	backend.current.State = StateCancelling
	return backend.current, nil
}

func TestExecuteDerivesIdempotencyKeyAndReturnsReadyURL(t *testing.T) {
	backend := &fakeBackend{
		current: Deployment{ID: "dep_1", State: StateBuilding},
		records: []Record{{Cursor: "2", Kind: "state", Stage: StageSystem, State: StateReady}},
	}
	request := testRequest()
	result, err := (Coordinator{Backend: backend}).Execute(context.Background(), request, WaitOptions{Mode: WaitTerminal}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "ready" || result.Deployment.URL == "" {
		t.Fatalf("result = %#v", result)
	}
	if backend.submitted.RequestID == "" || backend.submitted.RequestID != result.RequestID {
		t.Fatalf("request IDs = %q, %q", backend.submitted.RequestID, result.RequestID)
	}
	if result.Bundle == nil || result.Bundle.ArchiveDigest != "sha256:archive" {
		t.Fatalf("bundle = %#v", result.Bundle)
	}
}

func TestExecuteFiniteWaitReturnsResumablePendingReceipt(t *testing.T) {
	backend := &fakeBackend{current: Deployment{ID: "dep_1", State: StateBuilding, URL: "https://too-early.example"}, block: true}
	result, err := (Coordinator{Backend: backend}).Execute(context.Background(), testRequest(), WaitOptions{
		Mode: WaitTimeout, Timeout: time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "pending" || result.NextOperation != "status" || result.Deployment.URL != "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestObserverFailureStopsStream(t *testing.T) {
	backend := &fakeBackend{current: Deployment{ID: "dep_1", State: StateBuilding}, records: []Record{{Kind: "log", Stage: StageBuild}}}
	want := errors.New("stop")
	_, err := (Coordinator{Backend: backend}).Execute(context.Background(), testRequest(), WaitOptions{Mode: WaitTerminal}, func(Record) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

func TestDerivedRequestIDChangesWithProjectAndBundle(t *testing.T) {
	first := testRequest()
	second := testRequest()
	second.ProjectName = "other"
	if DerivedRequestID(first) == DerivedRequestID(second) {
		t.Fatal("project must be part of the request fingerprint")
	}
	second = testRequest()
	second.Archive.Digest = "sha256:other"
	if DerivedRequestID(first) == DerivedRequestID(second) {
		t.Fatal("archive must be part of the request fingerprint")
	}
}

func testRequest() Request {
	return Request{
		ProjectName: "example",
		Manifest: definition.Manifest{
			APIVersion: definition.ManifestAPIVersion,
			Kind:       definition.ManifestKind,
			Content:    definition.ManifestContent{Include: []string{"index.js"}},
			Build:      definition.ManifestBuild{Image: "node:22"},
			Run:        definition.ManifestRun{Command: []string{"node", "index.js"}, Port: 3000},
		},
		Archive: source.Archive{
			Path: "bundle.tar.gz", Digest: "sha256:archive", CompressedSize: 10,
			Content: source.Manifest{Digest: "sha256:content", FileCount: 2},
		},
	}
}
