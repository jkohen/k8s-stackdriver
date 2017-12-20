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
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/k8s-stackdriver/prometheus-to-sd/config"
	"github.com/GoogleCloudPlatform/k8s-stackdriver/prometheus-to-sd/translator"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/common/model"
	"golang.org/x/oauth2/google"
	monitoring "google.golang.org/api/monitoring/v3"
)

const (
	// TODO(jkohen): Make prometheus.io the default prefix.
	metricsPrefix = "custom.googleapis.com"
	// Built-in Prometheus metric exporting process start time.
	processStartTimeMetric = "process_start_time_seconds"
)

// Client allows sending batches of Prometheus samples to Stackdriver.
type Client struct {
	logger log.Logger

	url           string
	timeout       time.Duration
	metricsPrefix string
}

// NewClient creates a new Client.
func NewClient(logger log.Logger, url string, timeout time.Duration) *Client {
	return &Client{
		logger:        logger,
		url:           url,
		timeout:       timeout,
		metricsPrefix: metricsPrefix,
	}
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

func getStartTime(samples model.Samples) (time.Time, error) {
	// For cumulative metrics we need to know process start time.
	for _, sample := range samples {
		if sample.Metric[model.MetricNameLabel] == processStartTimeMetric {
			startSeconds := math.Trunc(float64(sample.Value))
			startNanos := 1000000000 * (float64(sample.Value) - startSeconds)
			return time.Unix(int64(startSeconds), int64(startNanos)), nil
		}
	}
	// If the process start time is not specified, assuming it's
	// the unix 1 second, because Stackdriver can't handle
	// unix zero or unix negative number.
	return time.Unix(1, 0),
		fmt.Errorf("Metric %s invalid or not defined. Cumulative will be inaccurate.",
			processStartTimeMetric)
}

// translateToStackdriver translates metrics in Prometheus format to Stackdriver format.
func (c *Client) translateToStackdriver(samples model.Samples) []*monitoring.TimeSeries {
	startTime, err := getStartTime(samples)
	if err != nil {
		level.Error(c.logger).Log("err", err)
		// Continue with the default startTime.
	}

	var ts []*monitoring.TimeSeries
	for _, sample := range samples {
		t, err := c.translateSample(sample, startTime)
		if err != nil {
			level.Warn(c.logger).Log(
				"msg", "error while processing metric",
				"metric", sample.Metric[model.MetricNameLabel],
				"err", err)
		} else {
			ts = append(ts, t)
		}
	}
	return ts
}

// getMetricType creates metric type name base on the metric prefix, and metric name.
func getMetricType(metricsPrefix string, sample *model.Sample) string {
	return fmt.Sprintf("%s/%s", metricsPrefix, sample.Metric[model.MetricNameLabel])
}

// Assumes that the sample type is Gauge, because Prometheus server doesn't pass the type.
func extractMetricKind(sample *model.Sample) string {
	return "GAUGE"
}

func getMonitoredResource(sample *model.Sample) (*monitoring.MonitoredResource, error) {
	return &monitoring.MonitoredResource{
		Labels: map[string]string{},
		Type:   "gke_container",
	}, nil
}

// getMetricLabels returns a Stackdriver label map from the sample.
// By convention it excludes the following Prometheus labels:
//  - model.MetricNameLabel
//  - Any with "_" prefix.
// TODO(jkohen): we probably need to exclude labels like "instance" and "job" too.
func getMetricLabels(sample *model.Sample) map[string]string {
	metricLabels := map[string]string{}
	for label, value := range sample.Metric {
		if label == model.MetricNameLabel || strings.HasPrefix(string(label), "_") {
			continue
		}
		metricLabels[string(label)] = string(value)
	}
	return metricLabels
}

func (c *Client) translateSample(sample *model.Sample,
	startTime time.Time) (*monitoring.TimeSeries, error) {

	interval := &monitoring.TimeInterval{
		EndTime: time.Now().UTC().Format(time.RFC3339),
	}
	metricKind := extractMetricKind(sample)
	if metricKind == "CUMULATIVE" {
		interval.StartTime = startTime.UTC().Format(time.RFC3339)
	}
	// Everything is a double in Prometheus. TODO(jkohen): if there is a
	// Stackdriver MetricDescriptor for this metric, use its value.
	valueType := "DOUBLE"
	point := &monitoring.Point{
		Interval: interval,
		Value: &monitoring.TypedValue{
			ForceSendFields: []string{},
		},
	}
	translator.setValueBaseOnSimpleType(sample.Value, valueType, point)

	monitoredResource, err := getMonitoredResource(sample)
	if err != nil {
		return &monitoring.TimeSeries{}, err
	}
	return &monitoring.TimeSeries{
		Metric: &monitoring.Metric{
			Labels: getMetricLabels(sample),
			Type:   getMetricType(c.metricsPrefix, sample),
		},
		Resource:   monitoredResource,
		MetricKind: metricKind,
		ValueType:  valueType,
		Points:     []*monitoring.Point{point},
	}, nil
}

// Write sends a batch of samples to Stackdriver via its HTTP API.
func (c *Client) Write(samples model.Samples) error {
	// TODO(jkohen): Construct from the target labels, to avoid dependency on GCE.
	gceConfig, err := config.GetGceConfig(c.metricsPrefix)
	if err != nil {
		return err
	}
	commonConfig := &config.CommonConfig{
		GceConfig: gceConfig,
	}
	// TODO(jkohen): reuse the client, if it makes sense.
	client, err := google.DefaultClient(
		context.Background(), monitoring.MonitoringReadScope)
	if err != nil {
		return err
	}
	stackdriverService, err := monitoring.New(client)
	stackdriverService.BasePath = c.url
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	ts := c.translateToStackdriver(samples)
	translator.SendToStackdriver(ctx, stackdriverService, commonConfig, ts)
	return nil
}

// Name identifies the client as an Stackdriver client.
func (c Client) Name() string {
	return "stackdriver"
}
