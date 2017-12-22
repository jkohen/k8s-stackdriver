# Overview

k8s-stackdriver/prometheus-remote-writer is a Prometheus server remote writer that can send metrics to Stackdriver. It includes a [default Kubernetes deployment](prometheus-service.yaml) to launch a working Prometheus server and remote writer that can be used in parallel to other Prometheus server instances.

# Configuration

prometheus-remote-writer requires Prometheus labels in a special format in order to populate the Stackdriver MonitoredResource. The [default Kubernetes deployment](prometheus-service.yaml) includes the necessary label mappings to support for Kubernetes.

prometheus-remote-writer relies on [Google Application Default Credentials](https://developers.google.com/identity/protocols/application-default-credentials) to authenticate with the Stackdriver Monitoring API. These should be present out of the box on Google Container Engine, and can be configured manually in other environments.

Other clouds that Prometheus support can be used. At the moment that requires manual customization of the label mappings and minor changes to the source code.
