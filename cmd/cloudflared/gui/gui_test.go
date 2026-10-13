package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuickTunnelURLRegex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard trycloudflare url",
			input:    "Your quick Tunnel has been created! Visit it at: https://orange-forest-1234.trycloudflare.com",
			expected: "https://orange-forest-1234.trycloudflare.com",
		},
		{
			name:     "json structured log line",
			input:    `{"time":"2026-10-12T10:00:00Z","level":"info","message":"Tunnel created at https://test-sample-456.trycloudflare.com"}`,
			expected: "https://test-sample-456.trycloudflare.com",
		},
		{
			name:     "no trycloudflare url",
			input:    "Starting cloudflared agent without tunnel",
			expected: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match := quickURLRegex.FindString(tt.input)
			assert.Equal(t, tt.expected, match)
		})
	}
}

func TestEdgeColoRegex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "ams location",
			input:    "Connection registered with protocol: quic connIndex=0 ip=198.41.200.1 location=AMS",
			expected: "AMS",
		},
		{
			name:     "ord location",
			input:    "connIndex=1 location=ORD ip=198.41.200.2",
			expected: "ORD",
		},
		{
			name:     "no location",
			input:    "Unrelated log event",
			expected: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches := coloRegex.FindStringSubmatch(tt.input)
			if tt.expected == "" {
				assert.Empty(t, matches)
			} else {
				require.GreaterOrEqual(t, len(matches), 2)
				assert.Equal(t, tt.expected, matches[1])
			}
		})
	}
}

func TestManagerLogBufferingAndFiltering(t *testing.T) {
	t.Parallel()

	logger := zerolog.Nop()
	mgr, err := NewManager(&logger)
	require.NoError(t, err)

	// Subscribe to logs
	logCh, unsubscribe := mgr.SubscribeLogs()
	defer unsubscribe()

	// Append test logs
	mgr.recordLog("INFO", "system", "Agent initialized")
	mgr.recordLog("WARN", "tunnel-1", "High latency detected")
	mgr.recordLog("ERROR", "network", "Failed to connect to edge")

	// Verify subscriber received events
	select {
	case entry := <-logCh:
		assert.Equal(t, "INFO", entry.Level)
		assert.Equal(t, "system", entry.Source)
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for log channel subscriber")
	}

	// Test filtering by level
	errorLogs := mgr.GetLogs(10, "ERROR", "")
	assert.Len(t, errorLogs, 1)
	assert.Equal(t, "ERROR", errorLogs[0].Level)

	// Test filtering by search term
	latencyLogs := mgr.GetLogs(10, "", "latency")
	assert.Len(t, latencyLogs, 1)
	assert.Equal(t, "WARN", latencyLogs[0].Level)

	// Test stats calculation
	stats := mgr.GetStats()
	assert.GreaterOrEqual(t, stats.TotalLogLines, 3)
}

func TestSimulateIngressMatch(t *testing.T) {
	t.Parallel()

	rules := []IngressRule{
		{Hostname: "api.example.com", Path: "/v1", Service: "http://localhost:3000"},
		{Hostname: "*.internal.org", Service: "http://localhost:8080"},
		{Service: "http_status:404"},
	}

	tests := []struct {
		name        string
		url         string
		expectedIdx int
		expectedSvc string
	}{
		{
			name:        "exact hostname and path match",
			url:         "https://api.example.com/v1/users",
			expectedIdx: 0,
			expectedSvc: "http://localhost:3000",
		},
		{
			name:        "wildcard hostname match",
			url:         "https://dashboard.internal.org/home",
			expectedIdx: 1,
			expectedSvc: "http://localhost:8080",
		},
		{
			name:        "fallback catch-all rule",
			url:         "https://unknown.domain.com/",
			expectedIdx: 2,
			expectedSvc: "http_status:404",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx, matched := SimulateIngressMatch(rules, tt.url)
			assert.Equal(t, tt.expectedIdx, idx)
			require.NotNil(t, matched)
			assert.Equal(t, tt.expectedSvc, matched.Service)
		})
	}
}

func TestReadAndWriteConfigFile(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.yml")

	validYaml := "tunnel: my-tunnel-123\ningress:\n  - hostname: test.domain.com\n    service: http://localhost:8080\n  - service: http_status:404\n"
	err := WriteConfigFile(cfgPath, validYaml)
	require.NoError(t, err)

	content, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, validYaml, string(content))

	// Test invalid yaml rejection
	badYaml := "tunnel: broken: [invalid\n"
	err = WriteConfigFile(cfgPath, badYaml)
	assert.Error(t, err)
}
