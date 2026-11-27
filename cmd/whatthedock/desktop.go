package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	whatthedock "github.com/allisonhere/whatthedock"
	"github.com/allisonhere/whatthedock/internal/desktop"
)

// installDesktopCommand is "whatthedock install-desktop": it puts the app
// icon and a launcher entry in the user's XDG data directory. The release
// installer (scripts/install.sh) runs it after copying the binary; `go
// install` users run it themselves.
func installDesktopCommand(stdout, stderr io.Writer) int {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(stderr, "whatthedock install-desktop: launcher entries are only supported on Linux")
		return 1
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintf(stderr, "whatthedock install-desktop: %v\n", err)
		return 1
	}
	if strings.Contains(exe, "go-build") {
		fmt.Fprintln(stderr, "whatthedock install-desktop: this is a temporary `go run` binary; build or install whatthedock first and run that")
		return 1
	}
	dataHome, err := desktop.DataHome()
	if err != nil {
		fmt.Fprintf(stderr, "whatthedock install-desktop: %v\n", err)
		return 1
	}
	return runInstallDesktop(stdout, stderr, exe, dataHome)
}

func runInstallDesktop(stdout, stderr io.Writer, exe, dataHome string) int {
	result, err := desktop.Install(desktop.Options{
		DataHome: dataHome,
		Exe:      exe,
		Icons:    map[int][]byte{256: whatthedock.Icon256, 512: whatthedock.Icon512},
	})
	if err != nil {
		fmt.Fprintf(stderr, "whatthedock install-desktop: %v\n", err)
		return 1
	}
	for _, icon := range result.Icons {
		fmt.Fprintln(stdout, "icon:     ", icon)
	}
	if result.KeptExisting {
		fmt.Fprintln(stdout, "launcher: ", result.DesktopFile, "(already there, left as it is)")
	} else {
		fmt.Fprintln(stdout, "launcher: ", result.DesktopFile)
	}
	refreshDesktopCaches(dataHome)
	return 0
}

// refreshDesktopCaches nudges the desktop to notice the new files. Both tools
// are optional and many desktops watch the directories anyway, so failures
// are ignored.
func refreshDesktopCaches(dataHome string) {
	if path, err := exec.LookPath("update-desktop-database"); err == nil {
		_ = exec.Command(path, "-q", filepath.Join(dataHome, "applications")).Run()
	}
	if path, err := exec.LookPath("gtk-update-icon-cache"); err == nil {
		_ = exec.Command(path, "-q", "-t", "-f", filepath.Join(dataHome, "icons", "hicolor")).Run()
	}
}
