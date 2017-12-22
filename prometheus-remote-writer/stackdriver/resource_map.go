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
	"github.com/prometheus/common/model"
	monitoring "google.golang.org/api/monitoring/v3"
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
