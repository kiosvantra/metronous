package cli

import (
	"fmt"
	"os"
	"strings"
)

type fileBackup struct {
	path   string
	data   []byte
	exists bool
}

func backupFile(path string) (*fileBackup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &fileBackup{path: path, exists: false}, nil
		}
		return nil, err
	}
	return &fileBackup{path: path, data: data, exists: true}, nil
}

func (b *fileBackup) restore(mode os.FileMode) error {
	if b == nil {
		return nil
	}
	if !b.exists {
		if err := os.Remove(b.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(b.path, b.data, mode)
}

func combineRollback(primary error, errs ...error) error {
	parts := []string{}
	for _, err := range errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	if len(parts) == 0 {
		return primary
	}
	return fmt.Errorf("%w (rollback errors: %s)", primary, strings.Join(parts, "; "))
}
