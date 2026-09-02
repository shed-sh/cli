package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"shed/internal/definition"
	"shed/internal/execution"
	"shed/internal/source"
)

// The fake server in these tests mirrors Bench's handler contract exactly:
// bundle register/archive/finalize/get under /v1/cli/bundles, deployment
// create with a required Idempotency-Key and a plain project slug, and the
// ordered events trail with an `after` sequence cursor.

func TestRemoteExecutionProtocolWalksBundleToDeployment(t *testing.T) {
	bundle := []byte("complete deterministic bundle")
	filename := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(filename, bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	var statusPolls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("authorization = %q on %s %s", got, request.Method, request.URL.Path)
		}
		switch request.Method + " " + request.URL.Path {
		case "POST /v1/cli/bundles":
			var body struct {
				Project       string `json:"project"`
				ProjectKind   string `json:"projectKind"`
				Kind          string `json:"kind"`
				ContentDigest string `json:"contentDigest"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Project != "example" || body.ProjectKind != "app" || body.Kind != "source" || body.ContentDigest != "sha256:content" {
				t.Errorf("register body = %+v", body)
			}
			response.WriteHeader(http.StatusCreated)
			writeJSON(t, response, map[string]any{"id": "bun_1", "status": "pending", "reused": false})
		case "PUT /v1/cli/bundles/bun_1/archive":
			got, err := io.ReadAll(request.Body)
			if err != nil || string(got) != string(bundle) {
				t.Errorf("uploaded %q, err %v", got, err)
			}
			writeJSON(t, response, map[string]string{"archiveDigest": "sha256:archive"})
		case "POST /v1/cli/bundles/bun_1/finalize":
			var body struct {
				ArchiveDigest string `json:"archiveDigest"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.ArchiveDigest != "sha256:archive" {
				t.Errorf("finalize digest = %q, err %v", body.ArchiveDigest, err)
			}
			response.WriteHeader(http.StatusAccepted)
			writeJSON(t, response, map[string]any{"id": "bun_1", "status": "validating"})
		case "GET /v1/cli/bundles/bun_1":
			status := "validating"
			if statusPolls.Add(1) > 1 {
				status = "ready"
			}
			writeJSON(t, response, map[string]any{"id": "bun_1", "status": status})
		case "POST /v1/cli/deployments":
			if got := request.Header.Get("Idempotency-Key"); got != "request-1" {
				t.Errorf("idempotency key = %q", got)
			}
			var body struct {
				Project     string          `json:"project"`
				ProjectKind string          `json:"projectKind"`
				BundleID    string          `json:"bundleId"`
				Runtime     json.RawMessage `json:"runtime"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Project != "example" || body.ProjectKind != "app" || body.BundleID != "bun_1" {
				t.Errorf("create body = %+v", body)
			}
			var runtime struct {
				Port int `json:"port"`
			}
			if err := json.Unmarshal(body.Runtime, &runtime); err != nil || runtime.Port != 3000 {
				t.Errorf("runtime = %s, err %v", body.Runtime, err)
			}
			response.WriteHeader(http.StatusAccepted)
			writeJSON(t, response, map[string]any{"id": "dep_1", "state": "accepted"})
		default:
			http.Error(response, "unexpected "+request.Method+" "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := New(server.URL, "token")
	client.pollInterval = time.Millisecond
	deployment, err := client.Submit(context.Background(), execution.Request{
		ProjectName: "example", RequestID: "request-1",
		Manifest: definition.Manifest{Run: definition.ManifestRun{Command: []string{"node", "index.js"}, Port: 3000}},
		Archive:  source.Archive{Path: filename, Digest: "sha256:archive", CompressedSize: int64(len(bundle)), Content: source.Manifest{Digest: "sha256:content", FileCount: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deployment.ID != "dep_1" || deployment.State != execution.StateAccepted {
		t.Fatalf("deployment = %#v", deployment)
	}
}

func TestRemoteExecutionReusesReadyBundleWithoutUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "POST /v1/cli/bundles":
			writeJSON(t, response, map[string]any{"id": "bun_1", "status": "ready", "reused": true})
		case "POST /v1/cli/deployments":
			writeJSON(t, response, map[string]any{"id": "dep_1", "state": "accepted"})
		default:
			t.Errorf("unexpected %s %s", request.Method, request.URL.Path)
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client := New(server.URL, "token")
	deployment, err := client.Submit(context.Background(), execution.Request{
		ProjectName: "example", RequestID: "request-1",
		Archive: source.Archive{Path: "unused", Digest: "sha256:archive", Content: source.Manifest{Digest: "sha256:content"}},
	})
	if err != nil || deployment.ID != "dep_1" {
		t.Fatalf("deployment = %#v, err %v", deployment, err)
	}
}

func TestRemoteExecutionStatusEventsAndCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /v1/cli/deployments/dep_1":
			writeJSON(t, response, map[string]any{"id": "dep_1", "state": "building"})
		case "GET /v1/cli/deployments/dep_1/events":
			if got := request.URL.Query().Get("after"); got != "2" {
				t.Errorf("after = %q", got)
			}
			writeJSON(t, response, map[string]any{"events": []map[string]any{
				{"sequence": 3, "type": "deployment.transitioned", "actor": "reconciler", "fromState": "health_checking", "toState": "ready", "createdAt": "2026-08-11T00:00:00Z"},
			}})
		case "POST /v1/cli/deployments/dep_1/cancel":
			response.WriteHeader(http.StatusAccepted)
			writeJSON(t, response, map[string]any{"id": "dep_1", "state": "cancelling"})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client := New(server.URL, "token")
	if current, err := client.Status(context.Background(), "dep_1"); err != nil || current.State != execution.StateBuilding {
		t.Fatalf("status = %#v, %v", current, err)
	}
	var records []execution.Record
	// follow=true returns as soon as the trail reaches a terminal state.
	cursor, err := client.Stream(context.Background(), "dep_1", "2", execution.StageAll, true, func(record execution.Record) error {
		records = append(records, record)
		return nil
	})
	if err != nil || cursor != "3" {
		t.Fatalf("stream cursor=%q err=%v", cursor, err)
	}
	if len(records) != 1 || records[0].State != execution.StateReady || records[0].Stage != execution.StageSystem {
		t.Fatalf("records = %#v", records)
	}
	if cancelled, err := client.Cancel(context.Background(), "dep_1"); err != nil || cancelled.State != execution.StateCancelling {
		t.Fatalf("cancel = %#v, %v", cancelled, err)
	}
}

func writeJSON(t *testing.T, response http.ResponseWriter, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Error(err)
	}
}
