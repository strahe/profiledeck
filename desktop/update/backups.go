package update

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type updateBackup struct {
	path    string
	modTime int64
}

func retainNewestUpdateBackups(directory string, limit int) error {
	if limit < 1 {
		return errors.New("update backup retention limit must be positive")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	backups := make([]updateBackup, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		backups = append(backups, updateBackup{path: filepath.Join(directory, entry.Name()), modTime: info.ModTime().UnixNano()})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].modTime == backups[j].modTime {
			return backups[i].path > backups[j].path
		}
		return backups[i].modTime > backups[j].modTime
	})
	if len(backups) <= limit {
		return nil
	}
	for _, backup := range backups[limit:] {
		if err := os.Remove(backup.path); err != nil {
			return err
		}
	}
	return nil
}
