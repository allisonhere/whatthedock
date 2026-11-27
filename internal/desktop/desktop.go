// Package desktop installs WhatTheDock's launcher entry and icon into the
// user's XDG data directory, so it shows up in application menus (Walker,
// rofi, GNOME, KDE, ...) with its icon.
package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IconName is the icon theme name the .desktop entry refers to.
const IconName = "whatthedock"

// marker is written into the .desktop file we generate. An existing entry
// without it is the user's own (a hand-written launcher, a customised
// terminal command) and is never overwritten.
const marker = "# Managed by `whatthedock install-desktop`; remove this line to keep your own edits."

// Options says what to install and where.
type Options struct {
	// DataHome is the XDG data directory ($XDG_DATA_HOME, ~/.local/share).
	DataHome string
	// Exe is the absolute path the launcher runs.
	Exe string
	// Icons maps a square size in pixels to its PNG data.
	Icons map[int][]byte
}

// Result reports what Install did.
type Result struct {
	Icons       []string // icon files written
	DesktopFile string   // the launcher entry, written or left alone
	// KeptExisting is set when a launcher entry that wasn't ours already
	// existed and was left as it was.
	KeptExisting bool
}

// DataHome is the user's XDG data directory.
func DataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// Install writes the icons and, unless the user already has their own, the
// launcher entry. It is safe to run repeatedly.
func Install(opts Options) (Result, error) {
	var result Result
	if opts.DataHome == "" || !filepath.IsAbs(opts.Exe) {
		return result, errors.New("desktop: need a data directory and an absolute executable path")
	}
	for size, data := range opts.Icons {
		path := filepath.Join(opts.DataHome, "icons", "hicolor", fmt.Sprintf("%dx%d", size, size), "apps", IconName+".png")
		if err := writeFile(path, data); err != nil {
			return result, err
		}
		result.Icons = append(result.Icons, path)
	}

	appsDir := filepath.Join(opts.DataHome, "applications")
	result.DesktopFile = filepath.Join(appsDir, IconName+".desktop")
	// Desktop file IDs are case-sensitive and people name this one by hand
	// ("WhatTheDock.desktop"): a second entry beside theirs would list the
	// app twice.
	for _, name := range []string{IconName + ".desktop", "WhatTheDock.desktop"} {
		path := filepath.Join(appsDir, name)
		existing, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, err
		}
		if !strings.Contains(string(existing), marker) {
			result.DesktopFile, result.KeptExisting = path, true
			return result, nil
		}
	}
	return result, writeFile(result.DesktopFile, []byte(Entry(opts.Exe)))
}

// Entry is the generated .desktop file for the binary at exe. It runs in a
// terminal (Terminal=true): the launcher picks the user's terminal, which on
// Omarchy is xdg-terminal-exec.
func Entry(exe string) string {
	return "[Desktop Entry]\n" + marker + "\n" +
		"Type=Application\n" +
		"Name=WhatTheDock\n" +
		"Comment=Terminal UI for Docker, Compose and remote hosts\n" +
		"Exec=" + execArg(exe) + "\n" +
		"Icon=" + IconName + "\n" +
		"Terminal=true\n" +
		"Categories=System;Monitor;\n" +
		"Keywords=docker;compose;containers;\n"
}

// execArg quotes path as one argument of a .desktop Exec key: double-quoted,
// with the characters that stay special inside quotes backslash-escaped, and
// then every backslash doubled for the file's own string escaping. A literal
// % would start a field code, so it is doubled.
func execArg(path string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range path {
		switch r {
		case '"', '`', '$', '\\':
			b.WriteString(`\\`) // an escaped backslash, itself doubled for the file
			if r == '\\' {
				b.WriteString(`\\`)
			} else {
				b.WriteRune(r)
			}
		case '%':
			b.WriteString("%%")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// writeFile replaces path atomically, so a launcher reading the file
// mid-install never sees half of it.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
