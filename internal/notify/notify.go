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

// Package notify sends a best-effort notification when a signature trips or
// a restoration completes (PLAN.md §5 Phase 4). Deliberately not a full
// Alertmanager rule-authoring path — that would mean standing up and
// configuring Alertmanager as a new moving part for comparatively little
// additional story value over a single webhook POST. A failure to notify
// must never fail or block a reconcile: notification is observability, not
// part of the mitigation correctness path (same reasoning as Metrics being
// nil-able — an optional dependency, not a required one).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TripEvent is everything a notification needs to describe a confirmed
// signature trip — the same data already available at the "cascade
// signature tripped" log line in Reconcile, not a new lookup.
type TripEvent struct {
	PolicyName      string
	PolicyNamespace string
	Signature       string
	Dependency      string
	Confidence      float64
	Evidence        string
}

// RestoreEvent describes a signature's restoration reaching its true
// pre-trip state — mirrors the data already available at each signature's
// own complete*Restore call site.
type RestoreEvent struct {
	PolicyName      string
	PolicyNamespace string
	Signature       string
}

// Notifier is deliberately two purpose-built methods, not one generic
// Notify(ctx, string) — a trip and a restore-completion carry different
// data and read better as distinct call sites than a caller having to
// format its own message string.
type Notifier interface {
	NotifyTrip(ctx context.Context, e TripEvent) error
	NotifyRestore(ctx context.Context, e RestoreEvent) error
}

// WebhookNotifier POSTs a Slack-compatible {"text": "..."} payload to a
// configured URL — the simplest format that both a real Slack incoming
// webhook and a generic HTTP endpoint (a test server, a custom relay) can
// consume without extra configuration.
type WebhookNotifier struct {
	URL    string
	Client *http.Client
}

// NewWebhookNotifier returns a WebhookNotifier with a bounded request
// timeout — a slow or hanging notification endpoint must never stall the
// reconcile loop waiting on it.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		URL:    url,
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (n *WebhookNotifier) NotifyTrip(ctx context.Context, e TripEvent) error {
	text := fmt.Sprintf(
		":rotating_light: *%s* tripped on `%s/%s` — dependency `%s`, confidence %.2f\n> %s",
		e.Signature, e.PolicyNamespace, e.PolicyName, e.Dependency, e.Confidence, e.Evidence,
	)
	return n.post(ctx, text)
}

func (n *WebhookNotifier) NotifyRestore(ctx context.Context, e RestoreEvent) error {
	text := fmt.Sprintf(
		":white_check_mark: *%s* restored to its pre-trip state on `%s/%s`",
		e.Signature, e.PolicyNamespace, e.PolicyName,
	)
	return n.post(ctx, text)
}

func (n *WebhookNotifier) post(ctx context.Context, text string) error {
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("marshal notification payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build notification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.Client.Do(req)
	if err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("notification endpoint returned %s", resp.Status)
	}
	return nil
}

var _ Notifier = (*WebhookNotifier)(nil)
