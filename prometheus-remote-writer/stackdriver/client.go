/*
Copyright 2017 Google Inc.

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

package stackdriver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/clock"

	gce "cloud.google.com/go/compute/metadata"
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

// TODO(jkohen): ensure these are sorted from more specific to less specific.
var resourceMappings = []resourceMap{
	{
		// This is just for testing, until the Kubernetes resource types are public.
		Type: "gke_container",
		LabelMap: map[string]string{
			"_kubernetes_project_id_or_name": "project_id",
			"_kubernetes_location":           "zone",
			"_kubernetes_cluster_name":       "cluster_name",
			"_kubernetes_namespace":          "namespace_id",
			"_kubernetes_pod_name":           "pod_id",
			"_kubernetes_pod_node_name":      "instance_id",
			"_kubernetes_pod_container_name": "container_name",
		},
	},
	{
		Type: "k8s_container",
		LabelMap: map[string]string{
			"_kubernetes_project_id_or_name": "project",
			"_kubernetes_location":           "location",
			"_kubernetes_cluster_name":       "cluster_name",
			"_kubernetes_namespace":          "namespace_name",
			"_kubernetes_pod_name":           "pod_name",
			"_kubernetes_pod_node_name":      "node_name",
			"_kubernetes_pod_container_name": "container_name",
		},
	},
	{
		Type: "k8s_pod",
		LabelMap: map[string]string{
			"_kubernetes_project_id_or_name": "project",
			"_kubernetes_location":           "location",
			"_kubernetes_cluster_name":       "cluster_name",
			"_kubernetes_namespace":          "namespace_name",
			"_kubernetes_pod_name":           "pod_name",
			"_kubernetes_pod_node_name":      "node_name",
		},
	},
	{
		Type: "k8s_node",
		LabelMap: map[string]string{
			"_kubernetes_project_id_or_name": "project",
			"_kubernetes_location":           "location",
			"_kubernetes_cluster_name":       "cluster_name",
			"_kubernetes_node_name":          "node_name",
		},
	},
}

type resourceMap struct {
	// The name of the Stackdriver MonitoredResource.
	Type string
	// Mapping from Prometheus to Stackdriver labels
	LabelMap map[string]string
}

func (m *resourceMap) Translate(metric model.Metric) *monitoring.MonitoredResource {
	result := monitoring.MonitoredResource{
		Type:   m.Type,
		Labels: map[string]string{},
	}
	for prometheusName, stackdriverName := range m.LabelMap {
		if value, ok := metric[model.LabelName(prometheusName)]; ok {
			result.Labels[string(stackdriverName)] = string(value)
		} else {
			return nil
		}
	}
	return &result
}

// Client allows sending batches of Prometheus samples to Stackdriver.
type Client struct {
	logger log.Logger
	clock  clock.Clock

	url           string
	timeout       time.Duration
	metricsPrefix string
}

// NewClient creates a new Client.
func NewClient(logger log.Logger, url string, timeout time.Duration) *Client {
	return &Client{
		logger:        logger,
		clock:         clock.Clock(clock.RealClock{}),
		url:           url,
		timeout:       timeout,
		metricsPrefix: metricsPrefix,
	}
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
		fmt.Errorf("metric %s invalid or not defined, cumulative will be inaccurate",
			processStartTimeMetric)
}

// translateToStackdriver translates metrics in Prometheus format to Stackdriver format.
func (c *Client) translateToStackdriver(samples model.Samples) []*monitoring.TimeSeries {
	startTime, err := getStartTime(samples)
	if err != nil {
		level.Error(c.logger).Log("sample_metric", samples[0], "err", err)
		// Continue with the default startTime.
	}

	// TODO(jkohen): See if it's possible for Prometheus to pass two points
	// for the same time series, which isn't accepted by the Stackdriver
	// Monitoring API.
	var ts []*monitoring.TimeSeries
	for _, sample := range samples {
		t, err := c.translateSample(sample, startTime)
		if err != nil {
			level.Warn(c.logger).Log(
				"msg", "error while processing metric",
				"metric", sample.Metric[model.MetricNameLabel],
				"err", err)
		} else {
			// TODO(jkohen): Remove once the whitelist goes away, or
			// at least make this controlled by a flag.
			if t.Resource.Type != "gke_container" {
				// The new k8s MonitoredResource types are still
				// behind a whitelist. Drop silently for now to
				// avoid errors in the logs.
				continue
			}
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

func getMonitoredResource(sample *model.Sample) *monitoring.MonitoredResource {
	for _, mapping := range resourceMappings {
		if resource := mapping.Translate(sample.Metric); resource != nil {
			return resource
		}
	}
	return nil
}

// getMetricLabels returns a Stackdriver label map from the sample.
// By convention it excludes the following Prometheus labels:
//  - model.MetricNameLabel
//  - Any with "_" prefix.
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

func setValue(value float64, valueType string, point *monitoring.Point) {
	if valueType == "INT64" {
		val := int64(value)
		point.Value.Int64Value = &val
		point.ForceSendFields = append(point.ForceSendFields, "Int64Value")
	} else if valueType == "DOUBLE" {
		point.Value.DoubleValue = &value
		point.ForceSendFields = append(point.ForceSendFields, "DoubleValue")
	} else if valueType == "BOOL" {
		const falseValueEpsilon = 0.001
		var val = math.Abs(value) > falseValueEpsilon
		point.Value.BoolValue = &val
		point.ForceSendFields = append(point.ForceSendFields, "BoolValue")
	}
}

func (c *Client) translateSample(sample *model.Sample,
	startTime time.Time) (*monitoring.TimeSeries, error) {

	value := float64(sample.Value)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return &monitoring.TimeSeries{},
			fmt.Errorf("cannot send value=%v to Stackdriver, skipping sample=%v", value, sample)
	}
	// TODO(jkohen): This should use the sample timestamp, if non-negative.
	interval := &monitoring.TimeInterval{EndTime: formatTime(c.clock.Now())}
	metricKind := extractMetricKind(sample)
	if metricKind == "CUMULATIVE" {
		interval.StartTime = formatTime(startTime)
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
	setValue(value, valueType, point)

	monitoredResource := getMonitoredResource(sample)
	if monitoredResource == nil {
		return &monitoring.TimeSeries{},
			fmt.Errorf("Cannot find MonitoredResource for %v", sample)
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
	if !gce.OnGCE() {
		return errors.New("not running on GCE")
	}

	project, err := gce.ProjectID()
	if err != nil {
		return fmt.Errorf("error while getting project id: %v", err)
	}

	// TODO(jkohen): Construct from configuration, to avoid dependency on
	// GCE.  See if we can get the project from the result of
	// FindDefaultCredentials. We can use them instead of the call to
	// DefaultClient below.
	// https://godoc.org/golang.org/x/oauth2/google#FindDefaultCredentials
	gceConfig := &config.GceConfig{
		Project:       project,
		MetricsPrefix: c.metricsPrefix,
	}
	commonConfig := &config.CommonConfig{
		GceConfig: gceConfig,
	}
	// TODO(jkohen): reuse the client, if it makes sense.
	client, err := google.DefaultClient(
		context.Background(), monitoring.MonitoringWriteScope)
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

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// Name identifies the client as an Stackdriver client.
func (c Client) Name() string {
	return "stackdriver"
}
