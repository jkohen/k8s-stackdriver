// Copyright 2017 The Prometheus Authors
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

// The main package for the Prometheus server executable.
package main

import (
	"flag"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/k8s-stackdriver/prometheus-remote-writer/stackdriver"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/prometheus/prompb"
)

type config struct {
	stackdriverURL string
	remoteTimeout  time.Duration
	listenAddr     string
	telemetryPath  string
}

var (
	receivedSamples = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "received_samples_total",
			Help: "Total number of received samples.",
		},
	)
	sentSamples = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sent_samples_total",
			Help: "Total number of processed samples sent to remote storage.",
		},
		[]string{"remote"},
	)
	failedSamples = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "failed_samples_total",
			Help: "Total number of processed samples which failed on send to remote storage.",
		},
		[]string{"remote"},
	)
	sentBatchDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "sent_batch_duration_seconds",
			Help:    "Duration of sample batch send calls to the remote storage.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"remote"},
	)
)

func init() {
	prometheus.MustRegister(receivedSamples)
	prometheus.MustRegister(sentSamples)
	prometheus.MustRegister(failedSamples)
	prometheus.MustRegister(sentBatchDuration)
}

func main() {
	cfg := parseFlags()
	http.Handle(cfg.telemetryPath, prometheus.Handler())

	logLevel := promlog.AllowedLevel{}
	logLevel.Set("debug")

	logger := promlog.New(logLevel)

	writers := buildClients(logger, cfg)
	serve(logger, cfg.listenAddr, writers)
}

func parseFlags() *config {
	cfg := &config{}

	flag.StringVar(&cfg.stackdriverURL, "stackdriver-url", "",
		"The URL of the remote Stackdriver server to send samples to. None, if empty.",
	)
	flag.DurationVar(&cfg.remoteTimeout, "send-timeout", 30*time.Second,
		"The timeout to use when sending samples to the remote storage.",
	)
	flag.StringVar(&cfg.listenAddr, "web.listen-address", ":9201", "Address to listen on for web endpoints.")
	flag.StringVar(&cfg.telemetryPath, "web.telemetry-path", "/metrics", "Address to listen on for web endpoints.")

	flag.Parse()

	return cfg
}

type writer interface {
	Write(samples model.Samples) error
	Name() string
}

func buildClients(logger log.Logger, cfg *config) []writer {
	var writers []writer
	if cfg.stackdriverURL != "" {
		c := stackdriver.NewClient(
			log.With(logger, "storage", "Stackdriver"),
			cfg.stackdriverURL,
			cfg.remoteTimeout,
		)
		writers = append(writers, c)
	}
	level.Info(logger).Log("Starting up...")
	return writers
}

func serve(logger log.Logger, addr string, writers []writer) error {
	http.HandleFunc("/write", func(w http.ResponseWriter, r *http.Request) {
		compressed, err := ioutil.ReadAll(r.Body)
		if err != nil {
			level.Error(logger).Log("msg", "Read error", "err", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		reqBuf, err := snappy.Decode(nil, compressed)
		if err != nil {
			level.Error(logger).Log("msg", "Decode error", "err", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var req prompb.WriteRequest
		if err := proto.Unmarshal(reqBuf, &req); err != nil {
			level.Error(logger).Log("msg", "Unmarshal error", "err", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		samples := protoToSamples(&req)
		receivedSamples.Add(float64(len(samples)))

		var wg sync.WaitGroup
		for _, w := range writers {
			wg.Add(1)
			go func(rw writer) {
				sendSamples(logger, rw, samples)
				wg.Done()
			}(w)
		}
		wg.Wait()
	})

	return http.ListenAndServe(addr, nil)
}

func protoToSamples(req *prompb.WriteRequest) model.Samples {
	var samples model.Samples
	for _, ts := range req.Timeseries {
		metric := make(model.Metric, len(ts.Labels))
		for _, l := range ts.Labels {
			metric[model.LabelName(l.Name)] = model.LabelValue(l.Value)
		}

		for _, s := range ts.Samples {
			samples = append(samples, &model.Sample{
				Metric:    metric,
				Value:     model.SampleValue(s.Value),
				Timestamp: model.Time(s.Timestamp),
			})
		}
	}
	return samples
}

func sendSamples(logger log.Logger, w writer, samples model.Samples) {
	begin := time.Now()
	err := w.Write(samples)
	duration := time.Since(begin).Seconds()
	if err != nil {
		level.Warn(logger).Log("msg", "Error sending samples to remote storage", "err", err, "storage", w.Name(), "num_samples", len(samples))
		failedSamples.WithLabelValues(w.Name()).Add(float64(len(samples)))
	}
	sentSamples.WithLabelValues(w.Name()).Add(float64(len(samples)))
	sentBatchDuration.WithLabelValues(w.Name()).Observe(duration)
}
