package legacy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const ManifestVersion = 1

type Manifest struct {
	Version int                      `json:"version"`
	Imports map[string]ManifestEntry `json:"imports"`
}

type ManifestEntry struct {
	Fingerprint string    `json:"fingerprint"`
	ImportedAt  time.Time `json:"imported_at"`
}

func LoadManifest(path string) (manifest *Manifest, err error) {
	manifest = &Manifest{Version: ManifestVersion, Imports: make(map[string]ManifestEntry)}
	payload, err := os.ReadFile(path)
	if os.IsNotExist(err) || path == "" {
		return manifest, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read legacy import manifest: %w", err)
	}
	if err = json.Unmarshal(payload, manifest); err != nil {
		return nil, fmt.Errorf("decode legacy import manifest: %w", err)
	}
	if manifest.Version != ManifestVersion {
		return nil, fmt.Errorf("unsupported legacy import manifest version %d", manifest.Version)
	}
	if manifest.Imports == nil {
		manifest.Imports = make(map[string]ManifestEntry)
	}
	return manifest, nil
}

func (manifest *Manifest) Contains(sessionID, fingerprint string) (contains bool) {
	entry, ok := manifest.Imports[sessionID]
	return ok && entry.Fingerprint == fingerprint
}

func (manifest *Manifest) Record(sessionID, fingerprint string) {
	manifest.Imports[sessionID] = ManifestEntry{Fingerprint: fingerprint, ImportedAt: time.Now().UTC()}
}

func (manifest *Manifest) Save(path string) (err error) {
	if path == "" {
		return fmt.Errorf("legacy import manifest path is required")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err = os.WriteFile(temporary, payload, 0o600); err != nil {
		return err
	}
	if err = os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
