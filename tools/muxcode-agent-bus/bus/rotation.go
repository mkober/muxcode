package bus

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RotationConfig controls memory file rotation behavior.
type RotationConfig struct {
	RetentionDays int // how long to keep archives (default: 30)
	ContextDays   int // how many days of history to include in context (default: 7)
}

// DefaultRotationConfig returns rotation defaults: 30-day retention, 7-day context.
func DefaultRotationConfig() RotationConfig {
	return RotationConfig{
		RetentionDays: 30,
		ContextDays:   7,
	}
}

// NeedsRotation returns true if the active memory file for a role was last
// modified before today (UTC). Returns false if the file doesn't exist.
func NeedsRotation(role string) bool {
	info, err := os.Stat(MemoryPath(role))
	if err != nil {
		return false
	}
	modDate := info.ModTime().Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	return modDate != today
}

// RotateMemory archives the active memory file to the per-role archive directory,
// using the file's modification date as the archive date. Also purges old archives.
func RotateMemory(role string, cfg RotationConfig) error {
	memPath := MemoryPath(role)
	info, err := os.Stat(memPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to rotate
		}
		return err
	}

	// Use modification date for the archive filename
	archiveDate := info.ModTime().Format("2006-01-02")
	archiveDir := MemoryArchiveDir(role)
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}

	archivePath := MemoryArchivePath(role, archiveDate)

	// If an archive already exists for this date, append to it
	if _, err := os.Stat(archivePath); err == nil {
		existing, readErr := os.ReadFile(archivePath)
		if readErr != nil {
			return readErr
		}
		newContent, readErr := os.ReadFile(memPath)
		if readErr != nil {
			return readErr
		}
		combined := string(existing) + string(newContent)
		if err := os.WriteFile(archivePath, []byte(combined), 0644); err != nil {
			return err
		}
		// Remove the active file
		return os.Remove(memPath)
	}

	// Atomic rename on POSIX
	if err := os.Rename(memPath, archivePath); err != nil {
		return err
	}

	// Purge old archives
	return PurgeOldArchives(role, cfg)
}

// PurgeOldArchives removes archive files older than RetentionDays.
func PurgeOldArchives(role string, cfg RotationConfig) error {
	dates, err := ListArchiveDates(role)
	if err != nil {
		return err
	}

	cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays).Format("2006-01-02")

	for _, date := range dates {
		if date < cutoff {
			path := MemoryArchivePath(role, date)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// ReadMemoryWithHistory reads the active memory file plus the last N days
// of archives, concatenated with most recent last.
func ReadMemoryWithHistory(role string, days int) (string, error) {
	var parts []string

	// Read archives within the window
	dates, err := ListArchiveDates(role)
	if err != nil {
		return "", err
	}

	cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	for _, date := range dates {
		if date >= cutoff {
			content, readErr := os.ReadFile(MemoryArchivePath(role, date))
			if readErr != nil {
				if os.IsNotExist(readErr) {
					continue
				}
				return "", readErr
			}
			if len(content) > 0 {
				parts = append(parts, string(content))
			}
		}
	}

	// Read active file (today)
	active, err := ReadMemory(role)
	if err != nil {
		return "", err
	}
	if active != "" {
		parts = append(parts, active)
	}

	return strings.Join(parts, "\n"), nil
}

// ListArchiveDates returns sorted date strings (YYYY-MM-DD) for all archives of a role.
func ListArchiveDates(role string) ([]string, error) {
	archiveDir := MemoryArchiveDir(role)
	dirEntries, err := os.ReadDir(archiveDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var dates []string
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasSuffix(name, ".md") {
			date := strings.TrimSuffix(name, ".md")
			// Validate date format
			if _, err := time.Parse("2006-01-02", date); err == nil {
				dates = append(dates, date)
			}
		}
	}

	sort.Strings(dates)
	return dates, nil
}

// AllMemoryEntriesWithArchives reads all memory files (active + archives)
// and returns their parsed entries.
func AllMemoryEntriesWithArchives() ([]MemoryEntry, error) {
	dir := MemoryDir()
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var all []MemoryEntry

	for _, de := range dirEntries {
		if de.IsDir() {
			// This is an archive directory — read all archive files
			role := de.Name()
			archiveDir := filepath.Join(dir, role)
			archiveEntries, err := os.ReadDir(archiveDir)
			if err != nil {
				continue
			}
			for _, ae := range archiveEntries {
				if ae.IsDir() || !strings.HasSuffix(ae.Name(), ".md") {
					continue
				}
				content, err := os.ReadFile(filepath.Join(archiveDir, ae.Name()))
				if err != nil {
					continue
				}
				entries := ParseMemoryEntries(string(content), role)
				all = append(all, entries...)
			}
			continue
		}

		name := de.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		role := strings.TrimSuffix(name, ".md")
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		entries := ParseMemoryEntries(string(content), role)
		all = append(all, entries...)
	}

	return all, nil
}

// ArchiveTotalSize returns the total size of all archive files for a role.
func ArchiveTotalSize(role string) int64 {
	archiveDir := MemoryArchiveDir(role)
	dirEntries, err := os.ReadDir(archiveDir)
	if err != nil {
		return 0
	}

	var total int64
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total
}

// ListMemoryRoles returns all roles that have either an active memory file
// or an archive directory.
func ListMemoryRoles() ([]string, error) {
	dir := MemoryDir()
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	seen := make(map[string]bool)
	for _, de := range dirEntries {
		if de.IsDir() {
			seen[de.Name()] = true
			continue
		}
		name := de.Name()
		if strings.HasSuffix(name, ".md") {
			seen[strings.TrimSuffix(name, ".md")] = true
		}
	}

	var roles []string
	for role := range seen {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles, nil
}
