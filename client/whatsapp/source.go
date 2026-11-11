package whatsapp

import (
	"database/sql"

	"whatsapp-client/core"
)

// Source bundles the message store and data dir for the sync daemon.
type Source struct {
	store   *MessageStore
	dataDir string
}

// NewSource builds the WhatsApp source around dataDir, falling back to a degraded store if SQLite cannot open.
func NewSource(dataDir string) *Source {
	store, err := NewMessageStore(dataDir)
	if err != nil {
		store = &MessageStore{db: &sql.DB{}}
	}
	return &Source{store: store, dataDir: dataDir}
}

// Close releases the message store so callers do not leak SQLite handles.
func (w *Source) Close() error {
	if w.store != nil {
		return w.store.Close()
	}
	return nil
}

// ResetData removes all WhatsApp data files (session and messages). Callers must stop the daemon first.
func ResetData(dataDir string) error {
	return core.DefaultReset(dataDir, []string{
		"messages.db",
		"messages.db-wal",
		"messages.db-shm",
		"whatsapp.db",
		"whatsapp.db-wal",
		"whatsapp.db-shm",
	})
}
