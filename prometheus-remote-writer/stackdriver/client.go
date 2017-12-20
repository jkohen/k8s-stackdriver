// Copyright 2013 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stackdriver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/common/model"
	"golang.org/x/net/context/ctxhttp"
)

const (
	putEndpoint     = "/api/put"
	contentTypeJSON = "application/json"
)

// Client allows sending batches of Prometheus samples to Stackdriver.
type Client struct {
	logger log.Logger

	url     string
	timeout time.Duration
}

// NewClient creates a new Client.
func NewClient(logger log.Logger, url string, timeout time.Duration) *Client {
	return &Client{
		logger:  logger,
		url:     url,
		timeout: timeout,
	}
}

// StoreSamplesRequest is used for building a JSON request for storing samples
// via the Stackdriver.
type StoreSamplesRequest struct {
	Metric    string            `json:"metric"`
	Timestamp int64             `json:"timestamp"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels"`
}

// labelsFromMetric translates Prometheus metric into Stackdriver labels.
func labelsFromMetric(m model.Metric) map[string]string {
	labels := make(map[string]string, len(m)-1)
	for l, v := range m {
		if l == model.MetricNameLabel {
			continue
		}
		labels[string(l)] = string(v)
	}
	return labels
}

// Write sends a batch of samples to Stackdriver via its HTTP API.
func (c *Client) Write(samples model.Samples) error {
	reqs := make([]StoreSamplesRequest, 0, len(samples))
	for _, s := range samples {
		v := float64(s.Value)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			level.Debug(c.logger).Log("msg", "cannot send value to Stackdriver, skipping sample", "value", v, "sample", s)
			continue
		}
		metric := string(s.Metric[model.MetricNameLabel])
		reqs = append(reqs, StoreSamplesRequest{
			Metric:    metric,
			Timestamp: s.Timestamp.Unix(),
			Value:     v,
			Labels:    labelsFromMetric(s.Metric),
		})
	}

	u, err := url.Parse(c.url)
	if err != nil {
		return err
	}

	u.Path = putEndpoint

	buf, err := json.Marshal(reqs)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	resp, err := ctxhttp.Post(ctx, http.DefaultClient, u.String(), contentTypeJSON, bytes.NewBuffer(buf))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// API returns status code 204 for successful writes.
	// http://opentsdb.net/docs/build/html/api_http/put.html
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	// API returns status code 400 on error, encoding error details in the
	// response content in JSON.
	buf, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var r map[string]int
	if err := json.Unmarshal(buf, &r); err != nil {
		return err
	}
	return fmt.Errorf("failed to write %d samples to Stackdriver, %d succeeded", r["failed"], r["success"])
}

// Name identifies the client as an Stackdriver client.
func (c Client) Name() string {
	return "stackdriver"
}
