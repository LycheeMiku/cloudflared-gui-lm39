package gui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

var (
	quickURLRegex  = regexp.MustCompile(`https://[a-zA-Z0-9.-]+\.trycloudflare\.com`)
	coloRegex      = regexp.MustCompile(`location=([A-Z]{3})`)
	ipRegex        = regexp.MustCompile(`ip=([0-9a-fA-F.:]+)`)
	connIndexRegex = regexp.MustCompile(`connIndex=([0-9]+)`)
)

// LogEntry represents an individual log line recorded by the manager.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Source    string    `json:"source"`
	Message   string    `json:"message"`
}

// TunnelInstance represents a managed tunnel process.
type TunnelInstance struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"` // "quick", "token", "named"
	Name         string     `json:"name"`
	Target       string     `json:"target,omitempty"`
	PublicURL    string     `json:"public_url,omitempty"`
	Status       string     `json:"status"` // "starting", "running", "stopped", "failed"
	PID          int        `json:"pid"`
	StartedAt    time.Time  `json:"started_at"`
	StoppedAt    *time.Time `json:"stopped_at,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	Colo         string     `json:"colo,omitempty"`

	cmd    *exec.Cmd          `json:"-"`
	cancel context.CancelFunc `json:"-"`
	mu     sync.RWMutex       `json:"-"`
	logs   []LogEntry         `json:"-"`
}

// Stats provides real-time metrics for the GUI dashboard.
type Stats struct {
	TotalTunnelsStarted int      `json:"total_tunnels_started"`
	ActiveTunnels       int      `json:"active_tunnels"`
	ActiveQuickTunnels  int      `json:"active_quick_tunnels"`
	TotalLogLines       int      `json:"total_log_lines"`
	ConnectedColos      []string `json:"connected_colos"`
	UptimeSeconds       int64    `json:"uptime_seconds"`
}

// Manager supervises tunnel subprocesses, captures logs, and provides event broadcasting.
type Manager struct {
	mu            sync.RWMutex
	tunnels       map[string]*TunnelInstance
	logs          []LogEntry
	maxLogs       int
	subscribers   map[chan LogEntry]struct{}
	subMu         sync.RWMutex
	startTime     time.Time
	logger        *zerolog.Logger
	executable    string
	colos         map[string]struct{}
	totalStarted  int
	isShuttingDown bool
}

// NewManager creates a new tunnel process supervisor.
func NewManager(log *zerolog.Logger) (*Manager, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve cloudflared executable: %w", err)
	}

	return &Manager{
		tunnels:     make(map[string]*TunnelInstance),
		logs:        make([]LogEntry, 0, 1000),
		maxLogs:     1000,
		subscribers: make(map[chan LogEntry]struct{}),
		startTime:   time.Now(),
		logger:      log,
		executable:  exePath,
		colos:       make(map[string]struct{}),
	}, nil
}

// StartQuickTunnel starts a zero-config quick tunnel pointing to a local address/port.
func (m *Manager) StartQuickTunnel(target string) (*TunnelInstance, error) {
	normalizedTarget := target
	if !strings.Contains(normalizedTarget, "://") {
		normalizedTarget = "http://localhost:" + strings.TrimPrefix(normalizedTarget, ":")
	}

	id := uuid.New().String()
	inst := &TunnelInstance{
		ID:        id,
		Type:      "quick",
		Name:      "Quick Tunnel (" + normalizedTarget + ")",
		Target:    normalizedTarget,
		Status:    "starting",
		StartedAt: time.Now(),
		logs:      make([]LogEntry, 0, 100),
	}

	ctx, cancel := context.WithCancel(context.Background())
	inst.cancel = cancel

	cmd := exec.CommandContext(ctx, m.executable, "tunnel", "--url", normalizedTarget)
	inst.cmd = cmd

	if err := m.launchProcess(inst); err != nil {
		cancel()
		return nil, err
	}

	return inst, nil
}

// StartTokenTunnel starts a tunnel using a Cloudflare Zero Trust token.
func (m *Manager) StartTokenTunnel(token string) (*TunnelInstance, error) {
	cleanToken := strings.TrimSpace(token)
	if cleanToken == "" {
		return nil, fmt.Errorf("token cannot be empty")
	}

	id := uuid.New().String()
	shortToken := cleanToken
	if len(shortToken) > 12 {
		shortToken = shortToken[:6] + "..." + shortToken[len(shortToken)-4:]
	}

	inst := &TunnelInstance{
		ID:        id,
		Type:      "token",
		Name:      "Token Tunnel (" + shortToken + ")",
		Status:    "starting",
		StartedAt: time.Now(),
		logs:      make([]LogEntry, 0, 100),
	}

	ctx, cancel := context.WithCancel(context.Background())
	inst.cancel = cancel

	cmd := exec.CommandContext(ctx, m.executable, "tunnel", "run", "--token", cleanToken)
	inst.cmd = cmd

	if err := m.launchProcess(inst); err != nil {
		cancel()
		return nil, err
	}

	return inst, nil
}

// StartNamedTunnel starts a tunnel configured locally by its name or ID.
func (m *Manager) StartNamedTunnel(name string) (*TunnelInstance, error) {
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		return nil, fmt.Errorf("tunnel name cannot be empty")
	}

	id := uuid.New().String()
	inst := &TunnelInstance{
		ID:        id,
		Type:      "named",
		Name:      cleanName,
		Status:    "starting",
		StartedAt: time.Now(),
		logs:      make([]LogEntry, 0, 100),
	}

	ctx, cancel := context.WithCancel(context.Background())
	inst.cancel = cancel

	cmd := exec.CommandContext(ctx, m.executable, "tunnel", "run", cleanName)
	inst.cmd = cmd

	if err := m.launchProcess(inst); err != nil {
		cancel()
		return nil, err
	}

	return inst, nil
}

func (m *Manager) launchProcess(inst *TunnelInstance) error {
	stdoutPipe, err := inst.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	stderrPipe, err := inst.cmd.StderrPipe()
	if err != nil {
		_ = stdoutPipe.Close()
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := inst.cmd.Start(); err != nil {
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return fmt.Errorf("start tunnel process: %w", err)
	}

	inst.PID = inst.cmd.Process.Pid

	m.mu.Lock()
	m.tunnels[inst.ID] = inst
	m.totalStarted++
	m.mu.Unlock()

	m.logger.Info().
		Str("id", inst.ID).
		Str("type", inst.Type).
		Int("pid", inst.PID).
		Msg("Tunnel process launched")

	m.recordLog("INFO", inst.Name, fmt.Sprintf("Tunnel process started (PID: %d)", inst.PID))

	go m.streamPipe(inst, stdoutPipe, "stdout")
	go m.streamPipe(inst, stderrPipe, "stderr")
	go m.watchProcessExit(inst)

	return nil
}

func (m *Manager) streamPipe(inst *TunnelInstance, pipe io.ReadCloser, pipeName string) {
	defer func() {
		_ = pipe.Close()
	}()

	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		level := "INFO"
		if strings.Contains(line, "ERR") || strings.Contains(line, "error") {
			level = "ERROR"
		} else if strings.Contains(line, "WRN") || strings.Contains(line, "warn") {
			level = "WARN"
		} else if strings.Contains(line, "DBG") || strings.Contains(line, "debug") {
			level = "DEBUG"
		}

		entry := LogEntry{
			Timestamp: time.Now(),
			Level:     level,
			Source:    inst.Name,
			Message:   trimmed,
		}

		inst.mu.Lock()
		inst.logs = append(inst.logs, entry)
		if len(inst.logs) > 200 {
			inst.logs = inst.logs[len(inst.logs)-200:]
		}

		// Detect quick tunnel public URL
		if inst.Type == "quick" && inst.PublicURL == "" {
			if match := quickURLRegex.FindString(trimmed); match != "" {
				inst.PublicURL = match
				inst.Status = "running"
				m.logger.Info().
					Str("tunnelID", inst.ID).
					Str("url", match).
					Msg("Discovered Quick Tunnel public URL")
			}
		}

		// Detect edge datacenter connection
		if match := coloRegex.FindStringSubmatch(trimmed); len(match) > 1 {
			colo := match[1]
			inst.Colo = colo
			inst.Status = "running"
			m.mu.Lock()
			m.colos[colo] = struct{}{}
			m.mu.Unlock()
		}

		if strings.Contains(trimmed, "Registered tunnel connection") {
			inst.Status = "running"
		}
		inst.mu.Unlock()

		m.appendLog(entry)
	}
}

func (m *Manager) watchProcessExit(inst *TunnelInstance) {
	err := inst.cmd.Wait()
	now := time.Now()

	inst.mu.Lock()
	inst.StoppedAt = &now
	if err != nil {
		if inst.Status != "stopped" {
			inst.Status = "failed"
			inst.ErrorMessage = err.Error()
		}
	} else {
		inst.Status = "stopped"
	}
	inst.mu.Unlock()

	msg := fmt.Sprintf("Tunnel process terminated (status: %s)", inst.Status)
	if inst.ErrorMessage != "" {
		msg += fmt.Sprintf(": %s", inst.ErrorMessage)
	}

	m.recordLog("INFO", inst.Name, msg)
	m.logger.Info().
		Str("id", inst.ID).
		Str("status", inst.Status).
		Msg("Tunnel process exited")
}

// StopTunnel stops a specific running tunnel instance by ID.
func (m *Manager) StopTunnel(id string) error {
	m.mu.RLock()
	inst, exists := m.tunnels[id]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("tunnel instance with ID %q not found", id)
	}

	inst.mu.Lock()
	if inst.Status == "stopped" || inst.Status == "failed" {
		inst.mu.Unlock()
		return nil
	}
	inst.Status = "stopped"
	cancel := inst.cancel
	cmd := inst.cmd
	inst.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	return nil
}

// StopAll stops all currently running tunnel instances.
func (m *Manager) StopAll() {
	m.mu.Lock()
	m.isShuttingDown = true
	var toStop []*TunnelInstance
	for _, inst := range m.tunnels {
		toStop = append(toStop, inst)
	}
	m.mu.Unlock()

	for _, inst := range toStop {
		_ = m.StopTunnel(inst.ID)
	}
}

// GetTunnels returns snapshots of all managed tunnel instances.
func (m *Manager) GetTunnels() []*TunnelInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*TunnelInstance, 0, len(m.tunnels))
	for _, inst := range m.tunnels {
		inst.mu.RLock()
		snapshot := &TunnelInstance{
			ID:           inst.ID,
			Type:         inst.Type,
			Name:         inst.Name,
			Target:       inst.Target,
			PublicURL:    inst.PublicURL,
			Status:       inst.Status,
			PID:          inst.PID,
			StartedAt:    inst.StartedAt,
			StoppedAt:    inst.StoppedAt,
			ErrorMessage: inst.ErrorMessage,
			Colo:         inst.Colo,
		}
		inst.mu.RUnlock()
		result = append(result, snapshot)
	}
	return result
}

// GetTunnel returns a snapshot of a single tunnel instance.
func (m *Manager) GetTunnel(id string) *TunnelInstance {
	m.mu.RLock()
	inst, exists := m.tunnels[id]
	m.mu.RUnlock()

	if !exists {
		return nil
	}

	inst.mu.RLock()
	defer inst.mu.RUnlock()
	return &TunnelInstance{
		ID:           inst.ID,
		Type:         inst.Type,
		Name:         inst.Name,
		Target:       inst.Target,
		PublicURL:    inst.PublicURL,
		Status:       inst.Status,
		PID:          inst.PID,
		StartedAt:    inst.StartedAt,
		StoppedAt:    inst.StoppedAt,
		ErrorMessage: inst.ErrorMessage,
		Colo:         inst.Colo,
	}
}

func (m *Manager) recordLog(level, source, message string) {
	m.appendLog(LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Source:    source,
		Message:   message,
	})
}

func (m *Manager) appendLog(entry LogEntry) {
	m.mu.Lock()
	m.logs = append(m.logs, entry)
	if len(m.logs) > m.maxLogs {
		m.logs = m.logs[len(m.logs)-m.maxLogs:]
	}
	m.mu.Unlock()

	m.subMu.RLock()
	defer m.subMu.RUnlock()
	for ch := range m.subscribers {
		select {
		case ch <- entry:
		default:
		}
	}
}

// GetLogs returns buffered logs with optional filtering.
func (m *Manager) GetLogs(limit int, levelFilter string, searchFilter string) []LogEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var filtered []LogEntry
	targetLevel := strings.ToUpper(strings.TrimSpace(levelFilter))
	targetSearch := strings.ToLower(strings.TrimSpace(searchFilter))

	for _, entry := range m.logs {
		if targetLevel != "" && targetLevel != "ALL" && entry.Level != targetLevel {
			continue
		}
		if targetSearch != "" && !strings.Contains(strings.ToLower(entry.Message), targetSearch) && !strings.Contains(strings.ToLower(entry.Source), targetSearch) {
			continue
		}
		filtered = append(filtered, entry)
	}

	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	return filtered
}

// SubscribeLogs adds a channel subscriber for real-time log streaming.
func (m *Manager) SubscribeLogs() (<-chan LogEntry, func()) {
	ch := make(chan LogEntry, 100)

	m.subMu.Lock()
	m.subscribers[ch] = struct{}{}
	m.subMu.Unlock()

	unsubscribe := func() {
		m.subMu.Lock()
		delete(m.subscribers, ch)
		m.subMu.Unlock()
		close(ch)
	}

	return ch, unsubscribe
}

// GetStats returns current system and tunnel metrics.
func (m *Manager) GetStats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	active := 0
	activeQuick := 0
	for _, t := range m.tunnels {
		t.mu.RLock()
		if t.Status == "running" || t.Status == "starting" {
			active++
			if t.Type == "quick" {
				activeQuick++
			}
		}
		t.mu.RUnlock()
	}

	colosList := make([]string, 0, len(m.colos))
	for colo := range m.colos {
		colosList = append(colosList, colo)
	}

	return Stats{
		TotalTunnelsStarted: m.totalStarted,
		ActiveTunnels:       active,
		ActiveQuickTunnels:  activeQuick,
		TotalLogLines:       len(m.logs),
		ConnectedColos:      colosList,
		UptimeSeconds:       int64(time.Since(m.startTime).Seconds()),
	}
}
