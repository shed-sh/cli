package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWaitReadyAcceptsAnyServingHTTPStatus(t *testing.T) {
	for _, status := range []int{302, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			result, err := (Docker{}).WaitReady(context.Background(), Instance{URL: server.URL}, time.Second)
			if err != nil || result.StatusCode != status {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestWaitReadyClassifiesServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "broken", http.StatusBadGateway) }))
	defer server.Close()
	_, err := (Docker{}).WaitReady(context.Background(), Instance{URL: server.URL}, time.Second)
	if err == nil || !strings.HasPrefix(err.Error(), "http_5xx:") {
		t.Fatalf("err=%v", err)
	}
}

func TestWaitReadyClassifiesTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	_, err := (Docker{}).WaitReady(context.Background(), Instance{URL: server.URL}, 20*time.Millisecond)
	if err == nil || !strings.HasPrefix(err.Error(), "readiness_timeout:") {
		t.Fatalf("err=%v", err)
	}
}
