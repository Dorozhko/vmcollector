package remotewrite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	vmzstd "github.com/VictoriaMetrics/VictoriaMetrics/lib/encoding/zstd"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/yura/vmcollector/internal/metric"
)

type Client struct {
	URL  string
	HTTP *http.Client
}

func New(address string, timeout time.Duration) (*Client, error) {
	if address == "" {
		return nil, fmt.Errorf("VictoriaMetrics API address is empty")
	}
	endpoint, err := normalizeEndpoint(address)
	if err != nil {
		return nil, err
	}
	return &Client{URL: endpoint, HTTP: &http.Client{Timeout: timeout}}, nil
}

func normalizeEndpoint(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", fmt.Errorf("VictoriaMetrics API address is empty")
	}

	if !strings.Contains(address, "://") {
		address = "http://" + address
	}

	u, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("invalid VictoriaMetrics API address: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("VictoriaMetrics API address must use http or https")
	}

	if u.Host == "" {
		return "", fmt.Errorf("VictoriaMetrics API address must contain host and port")
	}

	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/api/v1/write")
	u.Path = path + "/api/v1/write"
	u.RawQuery = ""
	u.Fragment = ""

	return u.String(), nil
}

func (c *Client) Close() {}

// Write sends a VictoriaMetrics Remote Write v1 request.
// It uses the same protobuf + zstd framing as the current vmagent VM protocol:
// Content-Type: application/x-protobuf
// Content-Encoding: zstd
// X-VictoriaMetrics-Remote-Write-Version: 1
func (c *Client) Write(ctx context.Context, samples []metric.Sample) error {
	if len(samples) == 0 {
		return nil
	}

	series := make(map[string]*prompb.TimeSeries)
	for _, s := range samples {
		labels := make([]prompb.Label, 0, len(s.Labels)+1)
		labels = append(labels, prompb.Label{Name: "__name__", Value: s.Name})
		for _, l := range s.SortedLabels() {
			labels = append(labels, prompb.Label{Name: l.Name, Value: l.Value})
		}
		key := ""
		for _, l := range labels {
			key += l.Name + "=" + l.Value + "\x00"
		}
		ts := series[key]
		if ts == nil {
			ts = &prompb.TimeSeries{Labels: labels}
			series[key] = ts
		}
		ts.Samples = append(ts.Samples, prompb.Sample{Value: s.Value, Timestamp: s.Timestamp})
	}

	wr := &prompb.WriteRequest{}
	for _, ts := range series {
		wr.Timeseries = append(wr.Timeseries, *ts)
	}

	raw := wr.MarshalProtobuf(nil)
	body := vmzstd.CompressLevel(nil, raw, 0)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "zstd")
	req.Header.Set("X-VictoriaMetrics-Remote-Write-Version", "1")
	req.Header.Set("User-Agent", "vmcollector")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("remote write request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("remote write returned HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
