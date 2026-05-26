package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func writeJSONFile(path string, payload any, indent bool) error {
	var (
		body []byte
		err  error
	)
	if indent {
		body, err = json.MarshalIndent(payload, "", "  ")
	} else {
		body, err = json.Marshal(payload)
	}
	if err != nil {
		return err
	}
	return atomicWriteFile(path, body, 0o644)
}

func atomicWriteFile(path string, body []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}
