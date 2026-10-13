package gui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rs/zerolog"
	yaml "gopkg.in/yaml.v3"
)

// NativeApp encapsulates the cross-platform Fyne desktop GUI application.
type NativeApp struct {
	app        fyne.App
	window     fyne.Window
	manager    *Manager
	logger     *zerolog.Logger
	version    string
	buildTime  string
	buildType  string
	executable string
}

// NewNativeApp creates a new instance of the cross-platform native GUI.
func NewNativeApp(manager *Manager, log *zerolog.Logger, version, buildTime, buildType string) (*NativeApp, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}

	a := app.NewWithID("com.cloudflare.cloudflared.gui")
	w := a.NewWindow("Cloudflared - Cloudflare Tunnel Control Center")
	w.Resize(fyne.NewSize(980, 680))
	w.SetMaster()

	return &NativeApp{
		app:        a,
		window:     w,
		manager:    manager,
		logger:     log,
		version:    version,
		buildTime:  buildTime,
		buildType:  buildType,
		executable: exePath,
	}, nil
}

// Run displays the native GUI window and starts the event loop.
func (na *NativeApp) Run() {
	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Dashboard", theme.HomeIcon(), na.buildDashboardTab()),
		container.NewTabItemWithIcon("Quick Tunnels", theme.MediaFastForwardIcon(), na.buildQuickTunnelTab()),
		container.NewTabItemWithIcon("Named Tunnels", theme.ComputerIcon(), na.buildNamedTunnelsTab()),
		container.NewTabItemWithIcon("Ingress Studio", theme.DocumentIcon(), na.buildIngressTab()),
		container.NewTabItemWithIcon("Live Logs", theme.MenuIcon(), na.buildLogsTab()),
		container.NewTabItemWithIcon("Diagnostics", theme.SearchIcon(), na.buildDiagnosticsTab()),
		container.NewTabItemWithIcon("Settings & Service", theme.SettingsIcon(), na.buildSettingsTab()),
	)

	tabs.SetTabLocation(container.TabLocationTop)

	na.window.SetContent(tabs)
	na.window.CenterOnScreen()
	na.window.ShowAndRun()
}

// -------------------------------------------------------------------------
// Tab 1: Dashboard
// -------------------------------------------------------------------------

func (na *NativeApp) buildDashboardTab() fyne.CanvasObject {
	statusTitle := widget.NewLabelWithStyle("Cloudflared Daemon Active", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	uptimeLabel := widget.NewLabel("Uptime: 0s")
	activeTunnelsLabel := widget.NewLabel("Active Tunnels: 0")
	memoryLabel := widget.NewLabel("Memory: 0 MB")
	edgeLabel := widget.NewLabel("Connected Colos: Standby")

	// Stat cards container
	statsBox := container.NewVBox(
		statusTitle,
		container.NewGridWithColumns(4,
			widget.NewCard("Tunnels", "0 Running", activeTunnelsLabel),
			widget.NewCard("Edge Status", "Optimal", edgeLabel),
			widget.NewCard("System", fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH), memoryLabel),
			widget.NewCard("Runtime", na.version, uptimeLabel),
		),
	)

	// Quick launch banner
	portEntry := widget.NewEntry()
	portEntry.SetPlaceHolder("8080")
	portEntry.SetText("8080")

	launchBtn := widget.NewButtonWithIcon("Launch Quick Tunnel", theme.MediaPlayIcon(), func() {
		target := strings.TrimSpace(portEntry.Text)
		if target == "" {
			target = "8080"
		}
		inst, err := na.manager.StartQuickTunnel(target)
		if err != nil {
			dialog.ShowError(err, na.window)
			return
		}
		dialog.ShowInformation("Quick Tunnel Started", fmt.Sprintf("Tunnel process started (PID: %d).\nPublic URL will appear in Quick Tunnels tab once edge connects.", inst.PID), na.window)
	})
	launchBtn.Importance = widget.HighImportance

	quickLaunchCard := widget.NewCard("Instant Port Sharing", "Expose any local port to the world with zero configuration",
		container.NewBorder(nil, nil, widget.NewLabel("http://localhost:"), launchBtn, portEntry),
	)

	// Active tunnels list
	tunnelsListContainer := container.NewVBox()
	refreshTunnelsList := func() {
		tunnelsListContainer.Objects = nil
		allTunnels := na.manager.GetTunnels()
		activeCount := 0

		for _, t := range allTunnels {
			if t.Status == "running" || t.Status == "starting" {
				activeCount++
				tunnelID := t.ID
				tunnelURL := t.PublicURL
				title := fmt.Sprintf("[%s] %s (PID: %d)", strings.ToUpper(t.Type), t.Name, t.PID)
				sub := fmt.Sprintf("Status: %s | Colo: %s", t.Status, t.Colo)
				if tunnelURL != "" {
					sub += fmt.Sprintf(" | URL: %s", tunnelURL)
				}

				stopBtn := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
					_ = na.manager.StopTunnel(tunnelID)
				})
				stopBtn.Importance = widget.DangerImportance

				copyBtn := widget.NewButtonWithIcon("Copy URL", theme.ContentCopyIcon(), func() {
					if tunnelURL != "" {
						na.window.Clipboard().SetContent(tunnelURL)
						dialog.ShowInformation("Copied", "Tunnel URL copied to clipboard!", na.window)
					}
				})

				actions := container.NewHBox(copyBtn, stopBtn)
				row := container.NewBorder(nil, nil, nil, actions,
					container.NewVBox(
						widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
						widget.NewLabel(sub),
					),
				)
				tunnelsListContainer.Add(row)
				tunnelsListContainer.Add(widget.NewSeparator())
			}
		}

		if activeCount == 0 {
			tunnelsListContainer.Add(widget.NewLabel("No active tunnels. Launch a Quick Tunnel above or start a Named Tunnel."))
		}
		activeTunnelsLabel.SetText(fmt.Sprintf("%d Running", activeCount))
	}

	runningTunnelsCard := widget.NewCard("Running Tunnels", "", container.NewVScroll(tunnelsListContainer))

	// Background ticker for status update
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			stats := na.manager.GetStats()
			uptimeStr := fmt.Sprintf("Uptime: %ds", stats.UptimeSeconds)
			edgeStr := "Colos: Connected"
			if len(stats.ConnectedColos) > 0 {
				edgeStr = "Colos: " + strings.Join(stats.ConnectedColos, ", ")
			}
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			memStr := fmt.Sprintf("%.1f MB", float64(m.Alloc)/(1024*1024))

			fyne.Do(func() {
				uptimeLabel.SetText(uptimeStr)
				edgeLabel.SetText(edgeStr)
				memoryLabel.SetText(memStr)
				refreshTunnelsList()
			})
		}
	}()

	refreshTunnelsList()

	return container.NewVBox(
		statsBox,
		quickLaunchCard,
		runningTunnelsCard,
	)
}

// -------------------------------------------------------------------------
// Tab 2: Quick Tunnels
// -------------------------------------------------------------------------

func (na *NativeApp) buildQuickTunnelTab() fyne.CanvasObject {
	targetEntry := widget.NewEntry()
	targetEntry.SetPlaceHolder("http://localhost:8080 or port 3000")
	targetEntry.SetText("http://localhost:8080")

	protocolSelect := widget.NewSelect([]string{"HTTP / Web Application", "TCP (Stream)", "SSH (Port 22)", "RDP (Port 3389)"}, nil)
	protocolSelect.SetSelected("HTTP / Web Application")

	statusCard := widget.NewCard("Current Quick Tunnel URL", "Connecting to Cloudflare edge...", nil)
	urlLabel := widget.NewLabel("No active Quick Tunnel")
	copyURLBtn := widget.NewButtonWithIcon("Copy URL", theme.ContentCopyIcon(), nil)
	copyURLBtn.Disable()
	openBrowserBtn := widget.NewButtonWithIcon("Open in Browser", theme.NavigateNextIcon(), nil)
	openBrowserBtn.Disable()
	stopCurrentBtn := widget.NewButtonWithIcon("Stop Quick Tunnel", theme.MediaStopIcon(), nil)
	stopCurrentBtn.Disable()
	stopCurrentBtn.Importance = widget.DangerImportance

	var currentQuickTunnelID string

	startBtn := widget.NewButtonWithIcon("Create Quick Tunnel", theme.MediaPlayIcon(), func() {
		target := strings.TrimSpace(targetEntry.Text)
		if target == "" {
			dialog.ShowError(fmt.Errorf("please provide a local port or address"), na.window)
			return
		}

		inst, err := na.manager.StartQuickTunnel(target)
		if err != nil {
			dialog.ShowError(err, na.window)
			return
		}

		currentQuickTunnelID = inst.ID
		urlLabel.SetText("Connecting to Cloudflare Edge... please wait a few seconds.")
		statusCard.SetSubTitle(fmt.Sprintf("Process PID: %d | Target: %s", inst.PID, inst.Target))
		stopCurrentBtn.Enable()
	})
	startBtn.Importance = widget.HighImportance

	stopCurrentBtn.OnTapped = func() {
		if currentQuickTunnelID != "" {
			_ = na.manager.StopTunnel(currentQuickTunnelID)
			currentQuickTunnelID = ""
			urlLabel.SetText("Tunnel stopped.")
			copyURLBtn.Disable()
			openBrowserBtn.Disable()
			stopCurrentBtn.Disable()
		}
	}

	copyURLBtn.OnTapped = func() {
		text := urlLabel.Text
		if strings.HasPrefix(text, "https://") {
			na.window.Clipboard().SetContent(text)
			dialog.ShowInformation("Copied", "Tunnel URL copied to clipboard!", na.window)
		}
	}

	openBrowserBtn.OnTapped = func() {
		text := urlLabel.Text
		if strings.HasPrefix(text, "https://") {
			u, err := url.Parse(text)
			if err == nil {
				_ = na.app.OpenURL(u)
			}
		}
	}

	// Watcher for discovering the quick tunnel URL
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if currentQuickTunnelID != "" {
				inst := na.manager.GetTunnel(currentQuickTunnelID)
				if inst != nil {
					if inst.PublicURL != "" {
						pubURL := inst.PublicURL
						subTitle := fmt.Sprintf("Online at %s | PID: %d", inst.Colo, inst.PID)
						fyne.Do(func() {
							urlLabel.SetText(pubURL)
							copyURLBtn.Enable()
							openBrowserBtn.Enable()
							statusCard.SetSubTitle(subTitle)
						})
					} else if inst.Status == "failed" || inst.Status == "stopped" {
						status := inst.Status
						fyne.Do(func() {
							urlLabel.SetText(fmt.Sprintf("Tunnel ended (%s)", status))
							copyURLBtn.Disable()
							openBrowserBtn.Disable()
							stopCurrentBtn.Disable()
						})
					}
				}
			}
		}
	}()

	actionRow := container.NewHBox(copyURLBtn, openBrowserBtn, stopCurrentBtn)
	statusCard.SetContent(container.NewVBox(
		urlLabel,
		actionRow,
	))

	form := container.NewVBox(
		widget.NewLabelWithStyle("Start a Zero-Config Quick Tunnel", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Instantly share a local development port or service via Cloudflare's global edge network."),
		widget.NewForm(
			widget.NewFormItem("Target Address / Port", targetEntry),
			widget.NewFormItem("Protocol", protocolSelect),
		),
		startBtn,
		widget.NewSeparator(),
		statusCard,
	)

	return container.NewVScroll(form)
}

// -------------------------------------------------------------------------
// Tab 3: Named Tunnels & Zero Trust Token
// -------------------------------------------------------------------------

func (na *NativeApp) buildNamedTunnelsTab() fyne.CanvasObject {
	tokenEntry := widget.NewMultiLineEntry()
	tokenEntry.SetPlaceHolder("Paste Cloudflare Zero Trust Tunnel token here (starts with eyJh...)")
	tokenEntry.Wrapping = fyne.TextWrapBreak

	var currentTokenTunnelID string
	stopTokenBtn := widget.NewButtonWithIcon("Stop Token Tunnel", theme.MediaStopIcon(), nil)
	stopTokenBtn.Disable()
	stopTokenBtn.Importance = widget.DangerImportance

	runTokenBtn := widget.NewButtonWithIcon("Run Tunnel with Token", theme.MediaPlayIcon(), func() {
		tok := strings.TrimSpace(tokenEntry.Text)
		if tok == "" {
			dialog.ShowError(fmt.Errorf("token cannot be empty"), na.window)
			return
		}
		inst, err := na.manager.StartTokenTunnel(tok)
		if err != nil {
			dialog.ShowError(err, na.window)
			return
		}
		currentTokenTunnelID = inst.ID
		stopTokenBtn.Enable()
		dialog.ShowInformation("Token Tunnel Started", fmt.Sprintf("Tunnel process launched (PID: %d). Check Live Logs tab for details.", inst.PID), na.window)
	})
	runTokenBtn.Importance = widget.HighImportance

	stopTokenBtn.OnTapped = func() {
		if currentTokenTunnelID != "" {
			_ = na.manager.StopTunnel(currentTokenTunnelID)
			currentTokenTunnelID = ""
			stopTokenBtn.Disable()
			dialog.ShowInformation("Stopped", "Token tunnel stopped successfully.", na.window)
		}
	}

	tokenSection := widget.NewCard("Cloudflare Zero Trust Token Runner", "Run a tunnel configured in the Cloudflare Zero Trust dashboard",
		container.NewVBox(
			tokenEntry,
			container.NewHBox(runTokenBtn, stopTokenBtn),
		),
	)

	// Named tunnels creation and routing
	newTunnelEntry := widget.NewEntry()
	newTunnelEntry.SetPlaceHolder("Tunnel name (e.g. office-vpn)")

	createTunnelBtn := widget.NewButtonWithIcon("Create Named Tunnel", theme.ContentAddIcon(), func() {
		name := strings.TrimSpace(newTunnelEntry.Text)
		if name == "" {
			dialog.ShowError(fmt.Errorf("please provide a tunnel name"), na.window)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, na.executable, "tunnel", "create", name)
		out, err := cmd.CombinedOutput()
		if err != nil {
			dialog.ShowError(fmt.Errorf("failed to create tunnel: %s (%v)", string(out), err), na.window)
			return
		}
		dialog.ShowInformation("Tunnel Created", fmt.Sprintf("Tunnel %q created successfully!\n%s", name, string(out)), na.window)
		newTunnelEntry.SetText("")
	})

	routeTunnelEntry := widget.NewEntry()
	routeTunnelEntry.SetPlaceHolder("Tunnel name")
	routeHostEntry := widget.NewEntry()
	routeHostEntry.SetPlaceHolder("Hostname (e.g. app.mycompany.com)")

	routeBtn := widget.NewButtonWithIcon("Route DNS", theme.NavigateNextIcon(), func() {
		tunnelName := strings.TrimSpace(routeTunnelEntry.Text)
		hostname := strings.TrimSpace(routeHostEntry.Text)
		if tunnelName == "" || hostname == "" {
			dialog.ShowError(fmt.Errorf("tunnel and hostname are required"), na.window)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, na.executable, "tunnel", "route", "dns", tunnelName, hostname)
		out, err := cmd.CombinedOutput()
		if err != nil {
			dialog.ShowError(fmt.Errorf("route DNS failed: %s (%v)", string(out), err), na.window)
			return
		}
		dialog.ShowInformation("DNS Routed", fmt.Sprintf("Successfully routed %s -> %s!\n%s", hostname, tunnelName, string(out)), na.window)
	})

	namedManagementSection := widget.NewCard("Named Tunnels & DNS Routing", "Manage local certificates and route domain hostnames",
		container.NewVBox(
			widget.NewLabelWithStyle("Create New Tunnel", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewBorder(nil, nil, nil, createTunnelBtn, newTunnelEntry),
			widget.NewSeparator(),
			widget.NewLabelWithStyle("Route DNS Hostname to Tunnel", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewGridWithColumns(2, routeTunnelEntry, routeHostEntry),
			routeBtn,
		),
	)

	return container.NewVScroll(container.NewVBox(
		tokenSection,
		namedManagementSection,
	))
}

// -------------------------------------------------------------------------
// Tab 4: Ingress Studio
// -------------------------------------------------------------------------

func (na *NativeApp) buildIngressTab() fyne.CanvasObject {
	yamlEditor := widget.NewMultiLineEntry()
	yamlEditor.Wrapping = fyne.TextWrapBreak
	yamlEditor.TextStyle = fyne.TextStyle{Monospace: true}

	cfgPathLabel := widget.NewLabel("Config Path: Loading...")
	cfgPath, cfgData, _ := ReadConfigFile()
	cfgPathLabel.SetText("Config Path: " + cfgPath)

	if cfgData != nil {
		d, _ := yaml.Marshal(cfgData)
		yamlEditor.SetText(string(d))
	}

	saveBtn := widget.NewButtonWithIcon("Save Configuration", theme.DocumentSaveIcon(), func() {
		text := yamlEditor.Text
		if err := WriteConfigFile(cfgPath, text); err != nil {
			dialog.ShowError(err, na.window)
			return
		}
		dialog.ShowInformation("Saved", "Configuration file saved successfully!", na.window)
	})
	saveBtn.Importance = widget.HighImportance

	// Route Match Simulator
	simUrlEntry := widget.NewEntry()
	simUrlEntry.SetPlaceHolder("e.g. https://api.example.com/v1/users")
	simResultLabel := widget.NewLabel("Enter a URL to test ingress routing match.")

	testMatchBtn := widget.NewButtonWithIcon("Test Route Match", theme.SearchIcon(), func() {
		_, updatedData, err := ReadConfigFile()
		if err != nil || updatedData == nil {
			dialog.ShowError(fmt.Errorf("failed to read current rules: %v", err), na.window)
			return
		}
		idx, matched := SimulateIngressMatch(updatedData.Ingress, simUrlEntry.Text)
		if matched != nil {
			simResultLabel.SetText(fmt.Sprintf("Matched Rule #%d:\nHostname: %s | Path: %s\nProxied to: %s", idx+1, matched.Hostname, matched.Path, matched.Service))
		} else {
			simResultLabel.SetText("No rule matched. Incoming request will result in 404.")
		}
	})

	simulatorCard := widget.NewCard("Route Match Simulator", "Verify which ingress rule handles incoming requests",
		container.NewVBox(
			container.NewBorder(nil, nil, nil, testMatchBtn, simUrlEntry),
			simResultLabel,
		),
	)

	editorCard := widget.NewCard("YAML Ingress Configuration", "Edit ingress rules and reverse proxy destinations",
		container.NewVBox(
			cfgPathLabel,
			saveBtn,
			yamlEditor,
		),
	)

	return container.NewVScroll(container.NewVBox(
		simulatorCard,
		editorCard,
	))
}

// -------------------------------------------------------------------------
// Tab 5: Live Logs
// -------------------------------------------------------------------------

func (na *NativeApp) buildLogsTab() fyne.CanvasObject {
	logText := widget.NewMultiLineEntry()
	logText.Wrapping = fyne.TextWrapBreak
	logText.TextStyle = fyne.TextStyle{Monospace: true}
	logText.Disable()

	levelSelect := widget.NewSelect([]string{"ALL", "INFO", "WARN", "ERROR", "DEBUG"}, nil)
	levelSelect.SetSelected("ALL")

	searchEntry := widget.NewEntry()
	searchEntry.SetPlaceHolder("Search logs...")

	clearBtn := widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), func() {
		logText.SetText("")
	})

	autoScrollCheck := widget.NewCheck("Auto-scroll", nil)
	autoScrollCheck.SetChecked(true)

	refreshLogs := func() {
		entries := na.manager.GetLogs(300, levelSelect.Selected, searchEntry.Text)
		var sb strings.Builder
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("[%s] [%s] [%s] %s\n", e.Timestamp.Format("15:04:05"), e.Level, e.Source, e.Message))
		}
		newLogs := sb.String()
		fyne.Do(func() {
			logText.SetText(newLogs)
		})
	}

	levelSelect.OnChanged = func(_ string) { refreshLogs() }
	searchEntry.OnChanged = func(_ string) { refreshLogs() }

	// Background polling for streaming updates
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			refreshLogs()
		}
	}()

	controls := container.NewHBox(
		widget.NewLabel("Level:"), levelSelect,
		widget.NewLabel("Search:"), searchEntry,
		autoScrollCheck,
		clearBtn,
	)

	return container.NewBorder(controls, nil, nil, nil, logText)
}

// -------------------------------------------------------------------------
// Tab 6: Diagnostics & Network
// -------------------------------------------------------------------------

func (na *NativeApp) buildDiagnosticsTab() fyne.CanvasObject {
	domainEntry := widget.NewEntry()
	domainEntry.SetPlaceHolder("cloudflare.com")
	domainEntry.SetText("cloudflare.com")

	typeSelect := widget.NewSelect([]string{"A", "AAAA", "CNAME", "TXT", "MX"}, nil)
	typeSelect.SetSelected("A")

	dnsResultText := widget.NewLabel("Click 'Execute DoH Query' to resolve domain via 1.1.1.1")

	runDnsBtn := widget.NewButtonWithIcon("Execute DoH Query", theme.SearchIcon(), func() {
		dnsResultText.SetText("Querying 1.1.1.1...")
		domain := domainEntry.Text
		qtype := typeSelect.Selected
		go func() {
			res := QueryDoH(domain, qtype)
			var msg string
			if res.Success {
				ans := strings.Join(res.Answers, "\n")
				msg = fmt.Sprintf("Resolution Latency: %v\nDomain: %s (%s)\nAnswers:\n%s", res.Latency, res.Domain, res.Type, ans)
			} else {
				msg = fmt.Sprintf("Query Failed (%v):\n%s", res.Latency, res.ErrorMsg)
			}
			fyne.Do(func() {
				dnsResultText.SetText(msg)
			})
		}()
	})
	runDnsBtn.Importance = widget.HighImportance

	dnsCard := widget.NewCard("1.1.1.1 Cloudflare DNS over HTTPS (DoH)", "Direct encrypted DNS query resolver test",
		container.NewVBox(
			container.NewGridWithColumns(2, domainEntry, typeSelect),
			runDnsBtn,
			dnsResultText,
		),
	)

	// Edge Probe
	probeResultText := widget.NewLabel("Click 'Probe Edge' to test TCP handshakes.")
	probeBtn := widget.NewButtonWithIcon("Probe Edge Points", theme.ViewRefreshIcon(), func() {
		probeResultText.SetText("Probing edge endpoints...")
		go func() {
			results := ProbeEdgeEndpoints()
			var sb strings.Builder
			for _, r := range results {
				if r.Online {
					sb.WriteString(fmt.Sprintf("[ONLINE] %s - Latency: %v\n", r.Endpoint, r.Latency))
				} else {
					sb.WriteString(fmt.Sprintf("[OFFLINE] %s - Error: %s\n", r.Endpoint, r.ErrorMsg))
				}
			}
			msg := sb.String()
			fyne.Do(func() {
				probeResultText.SetText(msg)
			})
		}()
	})

	edgeCard := widget.NewCard("Cloudflare Edge Latency Probe", "TCP/TLS handshake latency to Argo regional endpoints",
		container.NewVBox(
			probeBtn,
			probeResultText,
		),
	)

	return container.NewVScroll(container.NewVBox(
		dnsCard,
		edgeCard,
	))
}

// -------------------------------------------------------------------------
// Tab 7: Settings & Service
// -------------------------------------------------------------------------

func (na *NativeApp) buildSettingsTab() fyne.CanvasObject {
	serviceStatusLabel := widget.NewLabel("Checking service status...")

	refreshServiceStatus := func() {
		if runtime.GOOS == "windows" {
			cmd := exec.Command("sc", "query", "Cloudflared")
			out, err := cmd.CombinedOutput()
			if err == nil && strings.Contains(string(out), "RUNNING") {
				serviceStatusLabel.SetText("Windows Service Status: RUNNING")
			} else if err == nil && strings.Contains(string(out), "STOPPED") {
				serviceStatusLabel.SetText("Windows Service Status: STOPPED")
			} else {
				serviceStatusLabel.SetText("Windows Service Status: Not Installed")
			}
		} else {
			serviceStatusLabel.SetText(fmt.Sprintf("OS Service: %s (managed by system supervisor)", runtime.GOOS))
		}
	}

	startSvcBtn := widget.NewButton("Start Service", func() {
		cmd := exec.Command("sc", "start", "Cloudflared")
		_ = cmd.Run()
		refreshServiceStatus()
	})

	stopSvcBtn := widget.NewButton("Stop Service", func() {
		cmd := exec.Command("sc", "stop", "Cloudflared")
		_ = cmd.Run()
		refreshServiceStatus()
	})

	installSvcBtn := widget.NewButton("Install Service", func() {
		cmd := exec.Command(na.executable, "service", "install")
		out, err := cmd.CombinedOutput()
		if err != nil {
			dialog.ShowError(fmt.Errorf("service install: %s (%v)", string(out), err), na.window)
		} else {
			dialog.ShowInformation("Installed", "Cloudflared installed as a system service.", na.window)
		}
		refreshServiceStatus()
	})

	uninstallSvcBtn := widget.NewButton("Uninstall Service", func() {
		cmd := exec.Command(na.executable, "service", "uninstall")
		_ = cmd.Run()
		refreshServiceStatus()
	})

	refreshServiceStatus()

	serviceCard := widget.NewCard("Operating System Service", "Control cloudflared as a system daemon",
		container.NewVBox(
			serviceStatusLabel,
			container.NewHBox(startSvcBtn, stopSvcBtn, installSvcBtn, uninstallSvcBtn),
		),
	)

	cfgPath, _, _ := ReadConfigFile()
	envCard := widget.NewCard("System Information", "",
		container.NewVBox(
			widget.NewLabel(fmt.Sprintf("Cloudflared Version: %s", na.version)),
			widget.NewLabel(fmt.Sprintf("Go Runtime: %s (%s/%s)", runtime.Version(), runtime.GOOS, runtime.GOARCH)),
			widget.NewLabel(fmt.Sprintf("Executable Path: %s", na.executable)),
			widget.NewLabel(fmt.Sprintf("Config Path: %s", cfgPath)),
		),
	)

	return container.NewVScroll(container.NewVBox(
		serviceCard,
		envCard,
	))
}
