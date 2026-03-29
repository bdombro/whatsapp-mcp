package main

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	dataDirOnce  sync.Once
	dataDirValue string
)

func dataDir() string {
	dataDirOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dataDirValue = filepath.Join(home, ".local", "share", "whatsapp-cli")
	})
	return dataDirValue
}
