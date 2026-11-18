package main

import (
	_ "embed"
	"flag"
	"fmt"
	"strings"
	"time"

	"kbgitexplorer/internal/startup"
)

// The version lives in the VERSION file (build/version.sh X.Y.Z).
//
//go:embed VERSION
var versionFile string

var appVersion = strings.TrimSpace(versionFile)

const (
	appAuthor = "Allan Marillier"
)

var appName = "KrankyBear GitExplorer"
var appCopyright = buildCopyrightNotice()

func buildCopyrightNotice() string {
	const startYear = 2026
	currentYear := time.Now().Year()
	if currentYear <= startYear {
		return "Copyright (c) Allan Marillier, 2026"
	}
	return fmt.Sprintf("Copyright (c) Allan Marillier, 2026-%d", currentYear)
}

func main() {
	mesaFallbackFlag := flag.Bool(startup.MesaFallbackFlagName, false, "internal: relaunch flag for the Mesa3D OpenGL fallback (Windows only)")
	// Override the Mesa fallback's automatic rasterizer pick (see startup.chooseGalliumDriver): llvmpipe is the fast default,
	// softpipe the safe one. Accepted on every OS so one command line works everywhere; only acts on Windows.
	mesaCompatFlag := flag.Bool(startup.MesaCompatFlagName, false, "Windows only: force the Mesa software-OpenGL fallback's safe, slower softpipe rasterizer (use if the app hangs after the first frame on a VM with a conservative virtual CPU, e.g. Proxmox kvm64/qemu64)")
	mesaPerformanceFlag := flag.Bool(startup.MesaPerformanceFlagName, false, "Windows only: force the fast llvmpipe rasterizer even if this host's CPU looks like a conservative virtual CPU (only once you have confirmed it does not hang)")
	flag.Parse()

	// Windows only (no-op elsewhere): prefer real hardware OpenGL, falling
	// back to the bundled Mesa3D software renderer (relaunching once) only
	// if the hardware probe fails. Must run before app.NewWithID — GLFW
	// only supports one Init/Terminate cycle per process, shared with
	// Fyne's own driver (see internal/startup/opengl.go).
	startup.EnsureWindowsOpenGLReady(*mesaFallbackFlag, *mesaCompatFlag, *mesaPerformanceFlag)

	runExplorerApp()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
