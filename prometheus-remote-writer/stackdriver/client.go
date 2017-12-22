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
	"time"

	"k8s.io/apimachinery/pkg/util/clock"

	gce "cloud.google.com/go/compute/metadata"
	"github.com/GoogleCloudPlatform/k8s-stackdriver/prometheus-to-sd/config"
	api "github.com/GoogleCloudPlatform/k8s-stackdriver/prometheus-to-sd/translator"
	"github.com/go-kit/kit/log"
	"github.com/prometheus/common/model"
	"golang.org/x/oauth2/google"
	monitoring "google.golang.org/api/monitoring/v3"
)

const (
	// TODO(jkohen): Make prometheus.io the default prefix.
	metricsPrefix = "custom.googleapis.com"
)

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

	translator := NewTranslator(c.logger, c.clock, c.metricsPrefix)
	ts := translator.translateToStackdriver(samples)
	api.SendToStackdriver(ctx, stackdriverService, commonConfig, ts)
	return nil
}

// Name identifies the client as an Stackdriver client.
func (c Client) Name() string {
	return "stackdriver"
}
