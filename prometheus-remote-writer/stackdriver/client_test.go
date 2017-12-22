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
	"reflect"
	"testing"

	monitoring "google.golang.org/api/monitoring/v3"

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
}

func TestTranslateToStackdriver(t *testing.T) {
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
