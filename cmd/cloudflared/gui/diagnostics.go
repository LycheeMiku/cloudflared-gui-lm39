package gui

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/cloudflare/cloudflared/config"
)

// IngressRule represents a single routing rule.
type IngressRule struct {
	Hostname string `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	Path     string `json:"path,omitempty" yaml:"path,omitempty"`
	Service  string `json:"service" yaml:"service"`
}

// ConfigData represents the parsed config.yml structure.
type ConfigData struct {
	TunnelID string        `json:"tunnel,omitempty" yaml:"tunnel,omitempty"`
	Ingress  []IngressRule `json:"ingress,omitempty" yaml:"ingress,omitempty"`
}

// DNSQueryResult holds the result of a DoH query.
type DNSQueryResult struct {
	Domain   string
	Type     string
	Latency  time.Duration
	Success  bool
	Answers  []string
	RawJSON  string
	ErrorMsg string
}

// QueryDoH performs a 1.1.1.1 Cloudflare DNS over HTTPS query.
func QueryDoH(domain, qtype string) DNSQueryResult {
	if domain == "" {
		domain = "cloudflare.com"
	}
	if qtype == "" {
		qtype = "A"
	}

	start := time.Now()
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://1.1.1.1/dns-query?name=%s&type=%s", domain, qtype), nil)
	if err != nil {
		return DNSQueryResult{
			Domain:   domain,
			Type:     qtype,
			Latency:  time.Since(start),
			Success:  false,
			ErrorMsg: err.Error(),
		}
	}
	req.Header.Set("Accept", "application/dns-json")

	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return DNSQueryResult{
			Domain:   domain,
			Type:     qtype,
			Latency:  latency,
			Success:  false,
			ErrorMsg: err.Error(),
		}
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return DNSQueryResult{
			Domain:   domain,
			Type:     qtype,
			Latency:  latency,
			Success:  false,
			ErrorMsg: err.Error(),
		}
	}

	var parsed struct {
		Status int `json:"Status"`
		Answer []struct {
			Name string `json:"name"`
			Type int    `json:"type"`
			TTL  int    `json:"TTL"`
			Data string `json:"data"`
		} `json:"Answer"`
	}

	var answers []string
	if err := json.Unmarshal(body, &parsed); err == nil {
		for _, ans := range parsed.Answer {
			answers = append(answers, fmt.Sprintf("%s (TTL %d)", ans.Data, ans.TTL))
		}
	}

	return DNSQueryResult{
		Domain:  domain,
		Type:    qtype,
		Latency: latency,
		Success: true,
		Answers: answers,
		RawJSON: string(body),
	}
}

// EdgeProbeResult holds latency metrics for a Cloudflare Edge endpoint.
type EdgeProbeResult struct {
	Endpoint string
	Online   bool
	Latency  time.Duration
	ErrorMsg string
}

// ProbeEdgeEndpoints tests connectivity to regional Argo Edge servers.
func ProbeEdgeEndpoints() []EdgeProbeResult {
	endpoints := []string{
		"1.1.1.1:443",
		"1.0.0.1:443",
		"region1.v2.argotunnel.com:7844",
		"region2.v2.argotunnel.com:7844",
	}

	results := make([]EdgeProbeResult, 0, len(endpoints))
	for _, ep := range endpoints {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", ep, 2*time.Second)
		latency := time.Since(start)

		if err != nil {
			results = append(results, EdgeProbeResult{
				Endpoint: ep,
				Online:   false,
				Latency:  0,
				ErrorMsg: err.Error(),
			})
		} else {
			_ = conn.Close()
			results = append(results, EdgeProbeResult{
				Endpoint: ep,
				Online:   true,
				Latency:  latency,
			})
		}
	}
	return results
}

// ReadConfigFile reads and parses the active config.yml.
func ReadConfigFile() (string, *ConfigData, error) {
	cfgPath := config.FindDefaultConfigPath()
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		// Return template if not found
		return cfgPath, &ConfigData{
			TunnelID: "your-tunnel-name-or-uuid",
			Ingress: []IngressRule{
				{Hostname: "app.example.com", Service: "http://localhost:8080"},
				{Hostname: "api.example.com", Service: "http://localhost:3000"},
				{Service: "http_status:404"},
			},
		}, nil
	}

	var parsed ConfigData
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return cfgPath, nil, fmt.Errorf("parse config yaml: %w", err)
	}

	return cfgPath, &parsed, nil
}

// WriteConfigFile writes the YAML config to disk.
func WriteConfigFile(targetPath string, yamlText string) error {
	var testNode yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &testNode); err != nil {
		return fmt.Errorf("invalid YAML syntax: %w", err)
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Backup existing
	if _, err := os.Stat(targetPath); err == nil {
		_ = os.WriteFile(targetPath+".bak", []byte(yamlText), 0644)
	}

	return os.WriteFile(targetPath, []byte(yamlText), 0644)
}

// SimulateIngressMatch determines which ingress rule matches a given URL.
func SimulateIngressMatch(rules []IngressRule, rawURL string) (int, *IngressRule) {
	clean := strings.TrimSpace(rawURL)
	if clean == "" {
		return -1, nil
	}

	targetHost := clean
	targetPath := "/"

	if strings.Contains(clean, "://") {
		parts := strings.SplitN(clean, "://", 2)
		clean = parts[1]
	}
	slashIdx := strings.Index(clean, "/")
	if slashIdx != -1 {
		targetHost = clean[:slashIdx]
		targetPath = clean[slashIdx:]
	} else {
		targetHost = clean
	}

	for idx, rule := range rules {
		hostMatch := rule.Hostname == "" || rule.Hostname == "*" || rule.Hostname == targetHost
		if !hostMatch && strings.HasPrefix(rule.Hostname, "*.") {
			suffix := rule.Hostname[1:]
			if strings.HasSuffix(targetHost, suffix) {
				hostMatch = true
			}
		}

		pathMatch := rule.Path == "" || strings.HasPrefix(targetPath, rule.Path)

		if hostMatch && pathMatch {
			return idx, &rule
		}
	}

	return -1, nil
}
