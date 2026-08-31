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

package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	instantQueryPath = "/api/v1/query"
	maxBodyBytes     = 1 << 20
	defaultTimeout   = 5 * time.Second
)

// Client talks to Prometheus's HTTP API. Instant queries only: range windows
// belong in PromQL (rate(...[30s])), not in /api/v1/query_range.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient validates baseURL (http or https). If httpClient is nil, a client
// with a 5s timeout is used. Context on Query can still cancel sooner.
func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("prometheus URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("prometheus URL: scheme must be http or https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("prometheus URL: missing host")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		baseURL:    strings.TrimRight(u.String(), "/"),
		httpClient: httpClient,
	}, nil
}

// Query implements Querier against GET /api/v1/query.
func (c *Client) Query(ctx context.Context, promql string) (Snapshot, error) {
	if strings.TrimSpace(promql) == "" {
		return Snapshot{}, fmt.Errorf("prometheus query: empty PromQL")
	}

	u, err := url.Parse(c.baseURL + instantQueryPath)
	if err != nil {
		return Snapshot{}, fmt.Errorf("prometheus query: %w", err)
	}
	q := u.Query()
	q.Set("query", promql)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("prometheus query: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("prometheus query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return Snapshot{}, fmt.Errorf("prometheus query: read body: %w", err)
	}
	if len(body) > maxBodyBytes {
		return Snapshot{}, fmt.Errorf("prometheus query: response body exceeds %d bytes", maxBodyBytes)
	}

	if resp.StatusCode != http.StatusOK {
		// Real Prometheus sends the same {status,errorType,error} envelope on
		// query errors (400 bad_data, 422 execution, 503 unavailable) as it
		// does on a 200 with status=error — extract it here too, rather than
		// only on the 200 path, so a malformed detector PromQL logs the
		// actual reason instead of a raw JSON blob.
		if msg, ok := parseErrorBody(body); ok {
			return Snapshot{}, fmt.Errorf("prometheus query: HTTP %d: %s", resp.StatusCode, msg)
		}
		return Snapshot{}, fmt.Errorf("prometheus query: HTTP %d: %s", resp.StatusCode, truncateForErr(body))
	}

	return parseInstantResponse(body)
}

// parseErrorBody extracts Prometheus's {status:"error",errorType,error}
// envelope from a response body, if present. Returns ok=false if the body
// isn't that shape, so the caller can fall back to the raw truncated body.
func parseErrorBody(body []byte) (msg string, ok bool) {
	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil || resp.Status != "error" {
		return "", false
	}
	errMsg := resp.Error
	if errMsg == "" {
		errMsg = "status=" + resp.Status
	}
	if resp.ErrorType != "" {
		return fmt.Sprintf("%s: %s", resp.ErrorType, errMsg), true
	}
	return errMsg, true
}

type apiResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType"`
	Error     string          `json:"error"`
}

type queryData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

type vectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  samplePoint       `json:"value"`
}

// samplePoint is Prometheus's [timestamp, "value"] pair.
type samplePoint struct {
	Timestamp time.Time
	Value     float64
}

func (p *samplePoint) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("sample value: %w", err)
	}
	if len(raw) != 2 {
		return fmt.Errorf("sample value: want [timestamp, value], got %d elements", len(raw))
	}

	var ts float64
	if err := json.Unmarshal(raw[0], &ts); err != nil {
		return fmt.Errorf("sample timestamp: %w", err)
	}

	var valStr string
	if err := json.Unmarshal(raw[1], &valStr); err != nil {
		return fmt.Errorf("sample value: %w", err)
	}
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return fmt.Errorf("sample value %q: %w", valStr, err)
	}

	p.Timestamp = unixSeconds(ts)
	p.Value = v
	return nil
}

func parseInstantResponse(body []byte) (Snapshot, error) {
	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Snapshot{}, fmt.Errorf("prometheus query: decode response: %w", err)
	}
	if resp.Status != "success" {
		errMsg := resp.Error
		if errMsg == "" {
			errMsg = "status=" + resp.Status
		}
		if resp.ErrorType != "" {
			return Snapshot{}, fmt.Errorf("prometheus query: %s: %s", resp.ErrorType, errMsg)
		}
		return Snapshot{}, fmt.Errorf("prometheus query: %s", errMsg)
	}

	var data queryData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return Snapshot{}, fmt.Errorf("prometheus query: decode data: %w", err)
	}

	switch data.ResultType {
	case "vector":
		return parseVector(data.Result)
	case "scalar":
		return parseScalar(data.Result)
	case "matrix":
		return Snapshot{}, fmt.Errorf("prometheus query: matrix results are not supported; use an instant query so Prometheus owns the range-vector window")
	default:
		return Snapshot{}, fmt.Errorf("prometheus query: unsupported resultType %q", data.ResultType)
	}
}

func parseVector(raw json.RawMessage) (Snapshot, error) {
	var rows []vectorSample
	if err := json.Unmarshal(raw, &rows); err != nil {
		return Snapshot{}, fmt.Errorf("prometheus query: decode vector: %w", err)
	}
	out := Snapshot{Samples: make([]Sample, 0, len(rows))}
	for _, row := range rows {
		labels := row.Metric
		if labels == nil {
			labels = map[string]string{}
		} else {
			labels = maps.Clone(labels)
		}
		out.Samples = append(out.Samples, Sample{
			Labels:    labels,
			Value:     row.Value.Value,
			Timestamp: row.Value.Timestamp,
		})
	}
	return out, nil
}

func parseScalar(raw json.RawMessage) (Snapshot, error) {
	var pt samplePoint
	if err := json.Unmarshal(raw, &pt); err != nil {
		return Snapshot{}, fmt.Errorf("prometheus query: decode scalar: %w", err)
	}
	return Snapshot{Samples: []Sample{{
		Labels:    map[string]string{},
		Value:     pt.Value,
		Timestamp: pt.Timestamp,
	}}}, nil
}

func unixSeconds(ts float64) time.Time {
	sec := int64(ts)
	nsec := int64((ts - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC()
}

func truncateForErr(body []byte) string {
	const max = 256
	s := strings.TrimSpace(string(body))
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
