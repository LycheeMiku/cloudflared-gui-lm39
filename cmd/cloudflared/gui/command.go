package gui

import (
	"fmt"

	"github.com/urfave/cli/v2"

	"github.com/cloudflare/cloudflared/cmd/cloudflared/cliutil"
	"github.com/cloudflare/cloudflared/logger"
)

var (
	buildInfo      *cliutil.BuildInfo
	graceShutdownC chan struct{}
)

// Init registers global build info and shutdown signaling from main.
func Init(bInfo *cliutil.BuildInfo, shutdownC chan struct{}) {
	buildInfo = bInfo
	graceShutdownC = shutdownC
}

// Command returns the cli.Command definition for launching the native GUI.
func Command() *cli.Command {
	return &cli.Command{
		Name:    "gui",
		Aliases: []string{"ui", "dashboard"},
		Usage:   "Launch the cross-platform system native graphical user interface (GUI)",
		Description: `Starts the native desktop application window for managing Cloudflare Tunnels,
inspecting ingress routing rules, launching zero-config Quick Tunnels, viewing live streaming logs, and diagnosing network connectivity.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "token",
				Usage: "Automatically start a tunnel with this Cloudflare Zero Trust token upon launch",
			},
		},
		Action: cliutil.ConfiguredAction(runGUI),
	}
}

func runGUI(c *cli.Context) error {
	hideConsoleWindow()
	log := logger.CreateLoggerFromContext(c, logger.EnableTerminalLog)

	initialToken := c.String("token")

	manager, err := NewManager(log)
	if err != nil {
		return fmt.Errorf("initialize tunnel manager: %w", err)
	}

	version := "DEV"
	buildTime := "unknown"
	buildType := ""
	if buildInfo != nil {
		version = buildInfo.CloudflaredVersion
		buildType = buildInfo.BuildType
	}

	nativeApp, err := NewNativeApp(manager, log, version, buildTime, buildType)
	if err != nil {
		return fmt.Errorf("initialize native GUI: %w", err)
	}

	// If initial token provided, launch it automatically
	if initialToken != "" {
		log.Info().Msg("Starting tunnel with initial token flag")
		if _, err := manager.StartTokenTunnel(initialToken); err != nil {
			log.Error().Err(err).Msg("Failed to start tunnel from initial token")
		}
	}

	if graceShutdownC != nil {
		go func() {
			<-graceShutdownC
			log.Info().Msg("Received graceful shutdown event, closing native GUI...")
			nativeApp.app.Quit()
		}()
	}

	// Run native window on main thread
	nativeApp.Run()

	// Clean up all running tunnels
	manager.StopAll()
	log.Info().Msg("Cloudflared Native GUI closed cleanly")
	return nil
}
