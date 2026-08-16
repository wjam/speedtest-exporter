package main

import (
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/wjam/speedtest-exporter/internal"
)

func TestScrapeMetrics(t *testing.T) {
	setupSlog(t)

	oldExec := internal.ExecCommand
	t.Cleanup(func() {
		internal.ExecCommand = oldExec
	})
	internal.ExecCommand = func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{
			"GO_WANT_HELPER_PROCESS=1",
			fmt.Sprintf("TEST_OUTPUT=%s", `{"type":"result","timestamp":"2026-08-16T10:10:16Z","ping":{"jitter":0.90500000000000003,"latency":6.7320000000000002},"download":{"bandwidth":64536611,"bytes":406041603,"elapsed":6304},"upload":{"bandwidth":8745585,"bytes":31506078,"elapsed":3603},"isp":"Example","interface":{"internalIp":"1.1.1.1","name":"eth0","macAddr":"SNIP","isVpn":false,"externalIp":"8.8.8.8"},"server":{"id":12345,"name":"Example","location":"Bracknell","country":"United Kingdom","host":"speedtest-bracknell.example.com","port":8080,"ip":"8.8.8.8"},"result":{"id":"uuid","url":"https://www.speedtest.net/result/c/uuid"}}`),
		}
		return cmd
	}

	s := httptest.NewServer(app("", time.Second))
	t.Cleanup(s.Close)

	expected := fmt.Sprintf(`
# HELP speedtest_download_bytes Downloaded bytes
# TYPE speedtest_download_bytes gauge
speedtest_download_bytes 4.06041603e+08
# HELP speedtest_download_bytes_second Download speed in B/s
# TYPE speedtest_download_bytes_second gauge
speedtest_download_bytes_second 6.4536611e+07
# HELP speedtest_packet_loss_pct Packet loss percentage
# TYPE speedtest_packet_loss_pct gauge
speedtest_packet_loss_pct 0
# HELP speedtest_ping_jitter_seconds Ping jitter
# TYPE speedtest_ping_jitter_seconds gauge
speedtest_ping_jitter_seconds 0.000905
# HELP speedtest_ping_latency_seconds Ping latency
# TYPE speedtest_ping_latency_seconds gauge
speedtest_ping_latency_seconds 0.006732
# TYPE speedtest_scrape_duration_seconds gauge
speedtest_scrape_duration_seconds 0.003992941
# HELP speedtest_up Whether using speedtest-cli is succeeding or not
# TYPE speedtest_up gauge
speedtest_up 1
# HELP speedtest_upload_bytes Uploaded bytes
# TYPE speedtest_upload_bytes gauge
speedtest_upload_bytes 3.1506078e+07
# HELP speedtest_upload_bytes_second Upload speed in B/s
# TYPE speedtest_upload_bytes_second gauge
speedtest_upload_bytes_second 8.745585e+06
`)

	err := testutil.ScrapeAndCompare(
		fmt.Sprintf("%s/metrics", s.URL),
		strings.NewReader(expected),
		"speedtest_download_bytes",
		"speedtest_download_bytes_second",
		"speedtest_packet_loss_pct",
		"speedtest_ping_jitter_seconds",
		"speedtest_ping_latency_seconds",
		"speedtest_up",
		"speedtest_upload_bytes",
		"speedtest_upload_bytes_second",
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMetricsCachedBetweenCalls(t *testing.T) {
	setupSlog(t)

	oldExec := internal.ExecCommand
	t.Cleanup(func() {
		internal.ExecCommand = oldExec
	})

	callCount := atomic.Int32{}
	internal.ExecCommand = func(command string, args ...string) *exec.Cmd {
		callCount.Add(1)
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{
			"GO_WANT_HELPER_PROCESS=1",
			fmt.Sprintf("TEST_OUTPUT=%s", `{"type":"result","timestamp":"2026-08-16T10:10:16Z","ping":{"jitter":0.90500000000000003,"latency":6.7320000000000002},"download":{"bandwidth":64536611,"bytes":406041603,"elapsed":6304},"upload":{"bandwidth":8745585,"bytes":31506078,"elapsed":3603},"isp":"Example","interface":{"internalIp":"1.1.1.1","name":"eth0","macAddr":"SNIP","isVpn":false,"externalIp":"8.8.8.8"},"server":{"id":12345,"name":"Example","location":"Bracknell","country":"United Kingdom","host":"speedtest-bracknell.example.com","port":8080,"ip":"8.8.8.8"},"result":{"id":"uuid","url":"https://www.speedtest.net/result/c/uuid"}}`),
		}
		return cmd
	}

	s := httptest.NewServer(app("", time.Second))
	t.Cleanup(s.Close)

	for range 3 {
		resp, err := s.Client().Get(fmt.Sprintf("%s/metrics", s.URL))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Errorf("got status %d; want 200", resp.StatusCode)
		}
	}

	if callCount.Load() != 1 {
		t.Errorf("got call count %d; want 1", callCount.Load())
	}
}

func setupSlog(t *testing.T) {
	oldDefault := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(oldDefault)
	})
	slog.SetDefault(slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})))
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Println(os.Getenv("TEST_OUTPUT"))
	os.Exit(0)
}
