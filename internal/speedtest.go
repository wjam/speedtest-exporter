package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	ExecCommand = exec.Command
)

type speedtestCollector struct {
	mutex sync.Mutex

	cached      atomic.Pointer[cachedResult]
	cacheExpiry time.Duration

	up              *prometheus.Desc
	scrapeDuration  *prometheus.Desc
	latencySeconds  *prometheus.Desc
	jitterSeconds   *prometheus.Desc
	downloadBytes   *prometheus.Desc
	uploadBytes     *prometheus.Desc
	downloadedBytes *prometheus.Desc
	uploadedBytes   *prometheus.Desc
	packetLossPct   *prometheus.Desc

	serverID string
}

// NewSpeedtestCollectorWithOpts returns a collector, with a specified ServerID
func NewSpeedtestCollectorWithOpts(cacheExpiry time.Duration, server string) prometheus.Collector {
	const namespace = "speedtest"

	collector := &speedtestCollector{
		cacheExpiry: cacheExpiry,
		up: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "up"),
			"Whether using speedtest-cli is succeeding or not",
			nil,
			nil,
		),
		scrapeDuration: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "scrape_duration_seconds"),
			"Returns how long the probe took to complete in seconds",
			nil,
			nil,
		),
		latencySeconds: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "ping", "latency_seconds"),
			"Ping latency",
			nil,
			nil,
		),
		jitterSeconds: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "ping", "jitter_seconds"),
			"Ping jitter",
			nil,
			nil,
		),
		downloadBytes: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "download", "bytes_second"),
			"Download speed in B/s",
			nil,
			nil,
		),
		uploadBytes: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "upload", "bytes_second"),
			"Upload speed in B/s",
			nil,
			nil,
		),
		downloadedBytes: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "download", "bytes"),
			"Downloaded bytes",
			nil,
			nil,
		),
		uploadedBytes: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "upload", "bytes"),
			"Uploaded bytes",
			nil,
			nil,
		),
		packetLossPct: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "packet_loss_pct"),
			"Packet loss percentage",
			nil,
			nil,
		),

		serverID: server,
	}

	return collector

}

func (c *speedtestCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.up
	ch <- c.scrapeDuration
	ch <- c.latencySeconds
	ch <- c.jitterSeconds
	ch <- c.downloadBytes
	ch <- c.uploadBytes
	ch <- c.downloadedBytes
	ch <- c.uploadedBytes
	ch <- c.packetLossPct
}

func (c *speedtestCollector) Collect(ch chan<- prometheus.Metric) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	start := time.Now()
	success := 1
	defer func() {
		ch <- prometheus.MustNewConstMetric(c.scrapeDuration, prometheus.GaugeValue, time.Since(start).Seconds())
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, float64(success))
	}()

	result, err := c.cachedOrCollect()
	if err != nil {
		success = 0
		slog.ErrorContext(context.Background(), "failed to collect", "err", err)
	}

	ch <- prometheus.MustNewConstMetric(c.downloadBytes, prometheus.GaugeValue, result.Download.Bandwidth)
	ch <- prometheus.MustNewConstMetric(c.uploadBytes, prometheus.GaugeValue, result.Upload.Bandwidth)
	ch <- prometheus.MustNewConstMetric(c.latencySeconds, prometheus.GaugeValue, result.Ping.Latency/1000)
	ch <- prometheus.MustNewConstMetric(c.jitterSeconds, prometheus.GaugeValue, result.Ping.Jitter/1000)
	ch <- prometheus.MustNewConstMetric(c.uploadedBytes, prometheus.GaugeValue, result.Upload.Bytes)
	ch <- prometheus.MustNewConstMetric(c.downloadedBytes, prometheus.GaugeValue, result.Download.Bytes)
	ch <- prometheus.MustNewConstMetric(c.packetLossPct, prometheus.GaugeValue, result.PacketLoss)
}

func (c *speedtestCollector) cachedOrCollect() (speedtestResult, error) {
	cached := c.cached.Load()

	if cached != nil && cached.expire.After(time.Now()) {
		slog.DebugContext(context.Background(), "returning results from cache")
		return cached.result, nil
	}

	hot, err := c.collect()
	if err != nil {
		return hot, err
	}
	c.cached.CompareAndSwap(cached, &cachedResult{
		result: hot,
		expire: time.Now().Add(c.cacheExpiry),
	})
	return hot, nil
}

func (c *speedtestCollector) collect() (speedtestResult, error) {
	slog.DebugContext(context.Background(), "running speedtest")

	cmdParams := []string{"--accept-license", "--accept-gdpr", "--format", "json", "--unit", "B/s"}
	if c.serverID != "" {
		cmdParams = append(cmdParams, "-s", c.serverID)
	}

	cmd := ExecCommand("speedtest", cmdParams...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return speedtestResult{}, fmt.Errorf("speedtest failed: %w", err)
	}

	slog.DebugContext(context.Background(), "speedtest result", "result", stringerValue{&out})
	var result speedtestResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return speedtestResult{}, fmt.Errorf("failed to decode speedtest output: %w", err)
	}

	slog.InfoContext(context.Background(), "recorded", "url", result.Result.URL)
	return result, nil
}

type cachedResult struct {
	result speedtestResult
	expire time.Time
}

type speedtestResult struct {
	Ping       ping     `json:"ping"`
	Download   download `json:"download"`
	Upload     upload   `json:"upload"`
	PacketLoss float64  `json:"packetLoss"`
	Server     server   `json:"server"`
	Result     result   `json:"result"`
}

type ping struct {
	Jitter  float64 `json:"jitter"`
	Latency float64 `json:"latency"`
}

type download struct {
	Bandwidth float64 `json:"bandwidth"`
	Bytes     float64 `json:"bytes"`
}

type upload struct {
	Bandwidth float64 `json:"bandwidth"`
	Bytes     float64 `json:"bytes"`
}

type server struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Country  string `json:"country"`
	Host     string `json:"host"`
}

type result struct {
	URL string `json:"url"`
}
