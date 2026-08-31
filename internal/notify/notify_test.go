/*
Copyright 2026 Gideon Sanni.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSignature = "RetryStorm"

func TestNotifyTripPostsExpectedPayload(t *testing.T) {
	var gotBody []byte
	var gotMethod, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.URL)
	err := n.NotifyTrip(context.Background(), TripEvent{
		PolicyName:      "checkout-service",
		PolicyNamespace: "default",
		Signature:       testSignature,
		Dependency:      "inventory-service.default.svc.cluster.local",
		Confidence:      0.75,
		Evidence:        "dest_source_ratio=4.0 (threshold 3) retry_storm=true",
	})
	if err != nil {
		t.Fatalf("NotifyTrip: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %s, want application/json", gotContentType)
	}

	var payload map[string]string
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v (%s)", err, gotBody)
	}
	text, ok := payload["text"]
	if !ok {
		t.Fatalf("payload missing \"text\" key: %s", gotBody)
	}
	for _, want := range []string{testSignature, "default/checkout-service", "inventory-service.default.svc.cluster.local", "0.75", "dest_source_ratio=4.0"} {
		if !strings.Contains(text, want) {
			t.Errorf("notification text missing %q: %s", want, text)
		}
	}
}

func TestNotifyRestorePostsExpectedPayload(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.URL)
	err := n.NotifyRestore(context.Background(), RestoreEvent{
		PolicyName:      "checkout-service",
		PolicyNamespace: "default",
		Signature:       "FanOutAmplification",
	})
	if err != nil {
		t.Fatalf("NotifyRestore: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v (%s)", err, gotBody)
	}
	text := payload["text"]
	for _, want := range []string{"FanOutAmplification", "default/checkout-service", "restored"} {
		if !strings.Contains(text, want) {
			t.Errorf("notification text missing %q: %s", want, text)
		}
	}
}

func TestNotifyReturnsErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.URL)
	if err := n.NotifyTrip(context.Background(), TripEvent{Signature: testSignature}); err == nil {
		t.Fatal("expected an error on a 500 response, got nil")
	}
}

func TestNotifyReturnsErrorOnUnreachableEndpoint(t *testing.T) {
	n := NewWebhookNotifier("http://127.0.0.1:0")
	if err := n.NotifyTrip(context.Background(), TripEvent{Signature: testSignature}); err == nil {
		t.Fatal("expected an error posting to an unreachable endpoint, got nil")
	}
}
