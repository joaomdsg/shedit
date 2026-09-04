package cli

import (
	"os"
	"path/filepath"
	"strconv"
)

func DataDir() string {
	if v := os.Getenv("SHEDIT_DATA"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "shedit")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "shedit")
}

func SocketPath() string {
	if v := os.Getenv("SHEDIT_SOCKET"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "shedit.sock")
	}
	return filepath.Join(os.TempDir(), "shedit-"+strconv.Itoa(os.Getuid())+".sock")
}
