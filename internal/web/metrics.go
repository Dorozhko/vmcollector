package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	vmMetricsTimeout = 20 * time.Second
	vmSeriesLimit    = 5000
	vmResponseLimit  = 16 << 20
)

type vmAPIResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  string          `json:"error"`
}

type vmMetricItem struct {
	Name      string  `json:"name"`
	LastWrite float64 `json:"last_write"`
}

type vmMetricSeries struct {
	Labels map[string]string `json:"labels"`
}

type vmMetricDetails struct {
	Name        string              `json:"name"`
	SeriesCount int                 `json:"series_count"`
	Series      []vmMetricSeries    `json:"series"`
	LabelValues map[string][]string `json:"label_values"`
	Truncated   bool                `json:"truncated"`
}

func (s *Server) vmAPIBase() (string, error) {
	cfg := s.store.Get()
	address := strings.TrimSpace(cfg.Output.VictoriaMetrics.Address)
	if address == "" {
		return "", fmt.Errorf("VictoriaMetrics address is empty")
	}

	if !strings.Contains(address, "://") {
		address = "http://" + address
	}

	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid VictoriaMetrics address")
	}

	u.Path = strings.TrimRight(u.Path, "/")
	u.Path = strings.TrimSuffix(u.Path, "/api/v1")
	u.Path = strings.TrimSuffix(u.Path, "/prometheus")

	u.RawQuery = ""
	u.Fragment = ""

	return strings.TrimRight(u.String(), "/") + "/api/v1", nil
}

func (s *Server) vmAPIRequest(
	ctx context.Context,
	method string,
	endpoint string,
	params url.Values,
) (*vmAPIResponse, error) {
	base, err := s.vmAPIBase()
	if err != nil {
		return nil, err
	}

	target := base + endpoint
	if len(params) > 0 {
		target += "?" + params.Encode()
	}

	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(params.Encode())
		target = base + endpoint
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}

	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	client := &http.Client{Timeout: vmMetricsTimeout}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, vmResponseLimit))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("VictoriaMetrics returned HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	// Empty response is valid for VictoriaMetrics delete_series.
	if method == http.MethodPost &&
		endpoint == "/admin/tsdb/delete_series" &&
		len(strings.TrimSpace(string(raw))) == 0 {
		return &vmAPIResponse{
			Status: "success",
			Data:   json.RawMessage("null"),
		}, nil
	}

	var result vmAPIResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("invalid VictoriaMetrics response: %w", err)
	}

	if result.Status == "error" {
		return nil, fmt.Errorf("VictoriaMetrics: %s", result.Error)
	}

	return &result, nil
}

func validMetricName(name string) bool {
	if name == "" || len(name) > 200 {
		return false
	}

	for _, c := range name {
		if !((c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' || c == ':') {
			return false
		}
	}

	return true
}

func (s *Server) vmMetricsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), vmMetricsTimeout)
	defer cancel()

	namesResponse, err := s.vmAPIRequest(
		ctx,
		http.MethodGet,
		"/label/__name__/values",
		nil,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var names []string
	if err := json.Unmarshal(namesResponse.Data, &names); err != nil {
		http.Error(w, "invalid metric names response", http.StatusBadGateway)
		return
	}

	sort.Strings(names)

	query := url.Values{}
	query.Set("query", `max by (__name__) (timestamp({__name__!=""}))`)

	lastResponse, err := s.vmAPIRequest(
		ctx,
		http.MethodGet,
		"/query",
		query,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var vector struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		} `json:"result"`
	}

	if err := json.Unmarshal(lastResponse.Data, &vector); err != nil {
		http.Error(w, "invalid timestamp response", http.StatusBadGateway)
		return
	}

	lastWrites := make(map[string]float64)

	for _, item := range vector.Result {
		name := item.Metric["__name__"]
		if len(item.Value) != 2 {
			continue
		}

		ts, err := strconv.ParseFloat(fmt.Sprint(item.Value[0]), 64)
		if err == nil {
			lastWrites[name] = ts
		}
	}

	result := make([]vmMetricItem, 0, len(names))

	for _, name := range names {
		result = append(result, vmMetricItem{
			Name:      name,
			LastWrite: lastWrites[name],
		})
	}

	s.json(w, map[string]any{
		"metrics": result,
		"count":   len(result),
	})
}

func (s *Server) vmMetricDetailsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if !validMetricName(name) {
		http.Error(w, "invalid metric name", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), vmMetricsTimeout)
	defer cancel()

	params := url.Values{}
	params.Add("match[]", `{__name__="`+name+`"}`)

	response, err := s.vmAPIRequest(
		ctx,
		http.MethodGet,
		"/series",
		params,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var rawSeries []map[string]string
	if err := json.Unmarshal(response.Data, &rawSeries); err != nil {
		http.Error(w, "invalid series response", http.StatusBadGateway)
		return
	}

	details := vmMetricDetails{
		Name:        name,
		SeriesCount: len(rawSeries),
		Series:      make([]vmMetricSeries, 0),
		LabelValues: make(map[string][]string),
	}

	values := make(map[string]map[string]struct{})

	for i, labels := range rawSeries {
		if i >= vmSeriesLimit {
			details.Truncated = true
			break
		}

		details.Series = append(details.Series, vmMetricSeries{
			Labels: labels,
		})

		for key, value := range labels {
			if key == "__name__" {
				continue
			}

			if values[key] == nil {
				values[key] = make(map[string]struct{})
			}

			values[key][value] = struct{}{}
		}
	}

	for key, set := range values {
		list := make([]string, 0, len(set))
		for value := range set {
			list = append(list, value)
		}
		sort.Strings(list)
		details.LabelValues[key] = list
	}

	s.json(w, details)
}

func (s *Server) vmMetricDeleteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if !validMetricName(name) {
		http.Error(w, "invalid metric name", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), vmMetricsTimeout)
	defer cancel()

	params := url.Values{}
	params.Add("match[]", `{__name__="`+name+`"}`)

	// Verify the exact metric exists before deletion.
	check, err := s.vmAPIRequest(
		ctx,
		http.MethodGet,
		"/series",
		params,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var series []map[string]string
	if err := json.Unmarshal(check.Data, &series); err != nil {
		http.Error(w, "invalid series response", http.StatusBadGateway)
		return
	}

	if len(series) == 0 {
		http.Error(w, "metric not found", http.StatusNotFound)
		return
	}

	_, err = s.vmAPIRequest(
		ctx,
		http.MethodPost,
		"/admin/tsdb/delete_series",
		params,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	s.json(w, map[string]any{
		"ok":             true,
		"name":           name,
		"deleted_series": len(series),
		"message":        "Delete request accepted by VictoriaMetrics",
	})
}
