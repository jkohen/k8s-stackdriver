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
	"bytes"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/clock"

	monitoring "google.golang.org/api/monitoring/v3"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-kit/kit/log"
	"github.com/prometheus/common/model"
)

func TestResourceMapTranslate(t *testing.T) {
	r := resourceMap{
		Type: "my_type",
		LabelMap: map[string]string{
			"kube1": "sd1",
			"kube2": "sd2",
		},
	}
	// This metric is missing label "kube1".
	noMatchMetric := model.Metric{
		"ignored": "a",
		"kube2":   "b",
	}
	if resource := r.Translate(noMatchMetric); resource != nil {
		t.Errorf("Expected no match, matched %v", *resource)
	}
	matchMetric := model.Metric{
		"ignored": "a",
		"kube2":   "b",
		"kube1":   "c",
	}

	expectedResource := monitoring.MonitoredResource{
		Type: "my_type",
		Labels: map[string]string{
			"sd1": "c",
			"sd2": "b",
		},
	}
	if resource := r.Translate(matchMetric); resource == nil {
		t.Errorf("Expected %v, actual nil", expectedResource)
	} else if !reflect.DeepEqual(*resource, expectedResource) {
		t.Errorf("Expected %v, actual %v", expectedResource, *resource)
	}
}

func TestGetStartTime(t *testing.T) {
	{
		missingStartTime := model.Samples{
			&model.Sample{
				Metric: model.Metric{
					model.MetricNameLabel: "metric1",
				},
				Value: 1.0,
			},
		}
		startTime, err := getStartTime(missingStartTime)
		if err == nil {
			t.Fail()
		}
		if !startTime.After(time.Unix(0, 0)) {
			t.Errorf("Time must be positive: %v", startTime)
		}
	}
	{
		withStartTime := model.Samples{
			&model.Sample{
				Metric: model.Metric{
					model.MetricNameLabel: "process_start_time_seconds",
				},
				Value: 123.4,
			},
		}
		expectedTime := time.Unix(123, 400000000)
		startTime, err := getStartTime(withStartTime)
		if err != nil {
			t.Error(err)
		}
		if startTime != expectedTime {
			t.Errorf("Expected %v, actual %v", expectedTime, startTime)
		}
	}
}

var testKubernetesLabels = model.LabelSet{
	"_kubernetes_project_id_or_name": "a",
	"_kubernetes_location":           "b",
	"_kubernetes_cluster_name":       "c",
	"_kubernetes_namespace":          "d",
	"_kubernetes_pod_name":           "e",
	"_kubernetes_pod_node_name":      "f",
	"_kubernetes_pod_container_name": "g",
}
var testResourceLabels = map[string]string{
	"project_id":     "a",
	"zone":           "b",
	"cluster_name":   "c",
	"namespace_id":   "d",
	"pod_id":         "e",
	"instance_id":    "f",
	"container_name": "g",
}

// TestTranslateToStackdriver ensures the translation works correctly at a high
// level. Testing new fields here makes sense. For testing specific branches of
// each translation method, extend the dedicated test methods below.
func TestTranslateToStackdriver(t *testing.T) {
	sampleTime := time.Unix(1234, 567)
	output := &bytes.Buffer{}
	c := NewClient(log.NewLogfmtLogger(output), "", time.Duration(0))
	c.clock = clock.Clock(clock.NewFakeClock(sampleTime))

	v1 := 1.0
	v2 := 123.4
	samples := model.Samples{
		&model.Sample{
			Metric: model.Metric(
				testKubernetesLabels.Merge(
					model.LabelSet{
						model.MetricNameLabel: "metric1",
						"l1": "v1",
					})),
			Value: model.SampleValue(v1),
		},
		&model.Sample{
			Metric: model.Metric(
				testKubernetesLabels.Merge(
					model.LabelSet{
						model.MetricNameLabel: "process_start_time_seconds",
						"l2": "v2",
					})),
			Value: model.SampleValue(v2),
		},
	}
	ts := c.translateToStackdriver(samples)
	if ts == nil {
		t.Fatalf("Failed with error %v", output.String())
	}
	expectedTS := []*monitoring.TimeSeries{
		&monitoring.TimeSeries{
			Metric: &monitoring.Metric{
				Type:   "custom.googleapis.com/metric1",
				Labels: map[string]string{"l1": "v1"},
			},
			Resource: &monitoring.MonitoredResource{
				Type:   "gke_container",
				Labels: testResourceLabels,
			},
			MetricKind: "GAUGE",
			ValueType:  "DOUBLE",
			Points: []*monitoring.Point{
				{
					Interval: &monitoring.TimeInterval{
						EndTime: formatTime(sampleTime),
					},
					Value: &monitoring.TypedValue{
						DoubleValue:     &v1,
						ForceSendFields: []string{},
					},
					ForceSendFields: []string{"DoubleValue"},
				}},
		},
		&monitoring.TimeSeries{
			Metric: &monitoring.Metric{
				Type:   "custom.googleapis.com/process_start_time_seconds",
				Labels: map[string]string{"l2": "v2"},
			},
			Resource: &monitoring.MonitoredResource{
				Type:   "gke_container",
				Labels: testResourceLabels,
			},
			MetricKind: "GAUGE",
			ValueType:  "DOUBLE",
			Points: []*monitoring.Point{
				{
					Interval: &monitoring.TimeInterval{
						EndTime: formatTime(sampleTime),
					},
					Value: &monitoring.TypedValue{
						DoubleValue:     &v2,
						ForceSendFields: []string{},
					},
					ForceSendFields: []string{"DoubleValue"},
				}},
		},
	}
	if !reflect.DeepEqual(expectedTS, ts) {
		t.Errorf("Expected %v, actual %v", spew.Sdump(expectedTS), spew.Sdump(ts))
	}
}

func TestTranslateSample(t *testing.T) {
}

func TestGetMetricType(t *testing.T) {
}

func TestExtractMetricKind(t *testing.T) {
}

func TestGetMonitoredResource(t *testing.T) {
}

func TestGetMetricLabels(t *testing.T) {
}

func TestSetValue(t *testing.T) {
}

func TestWrite(t *testing.T) {
}
