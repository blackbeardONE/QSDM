//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func configureAutoStart(enabled bool, executable string) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	directory := filepath.Join(configDir, "autostart")
	path := filepath.Join(directory, "qsdm-edge-control.desktop")
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	escaped := strings.ReplaceAll(executable, `"`, `\"`)
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=QSDM Edge Control
Comment=Start the saved QSDM Agent or Relay
Exec="%s" --no-open --auto-start
Terminal=false
X-GNOME-Autostart-enabled=true
`, escaped)
	return writePrivateFile(path, []byte(content))
}
