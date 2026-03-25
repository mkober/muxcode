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

// needsRotationAt checks if a specific memory file needs rotation.
func needsRotationAt(memPath string) bool {
	info, err := os.Stat(memPath)
	if err != nil {
		return false
	}
	modDate := info.ModTime().Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	return modDate != today
}

// rotateMemoryAt archives a memory file to the given archive directory.
func rotateMemoryAt(memPath, archiveDir string, cfg RotationConfig) error {
	info, err := os.Stat(memPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	archiveDate := info.ModTime().Format("2006-01-02")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}

	archivePath := filepath.Join(archiveDir, archiveDate+".md")

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
		return os.Remove(memPath)
	}

	if err := os.Rename(memPath, archivePath); err != nil {
		return err
	}

	return purgeOldArchivesAt(archiveDir, cfg)
}

// purgeOldArchivesAt removes archive files older than RetentionDays from a directory.
func purgeOldArchivesAt(archiveDir string, cfg RotationConfig) error {
	dirEntries, err := os.ReadDir(archiveDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays).Format("2006-01-02")

	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasSuffix(name, ".md") {
			date := strings.TrimSuffix(name, ".md")
			if _, err := time.Parse("2006-01-02", date); err == nil {
				if date < cutoff {
					_ = os.Remove(filepath.Join(archiveDir, name))
				}
			}
		}
	}
	return nil
}

// readMemoryWithHistoryAt reads active + archive history from a specific memory directory.
func readMemoryWithHistoryAt(memDir, role string, days int) (string, error) {
	var parts []string

	// Determine paths
	var memPath string
	if role == "shared" {
		memPath = filepath.Join(memDir, "shared.md")
	} else {
		memPath = filepath.Join(memDir, role+".md")
	}
	archiveDir := filepath.Join(memDir, role)

	// Read archives within the window
	dirEntries, err := os.ReadDir(archiveDir)
	if err == nil {
		cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
		var dates []string
		for _, de := range dirEntries {
			if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") {
				continue
			}
			date := strings.TrimSuffix(de.Name(), ".md")
			if _, parseErr := time.Parse("2006-01-02", date); parseErr == nil {
				if date >= cutoff {
					dates = append(dates, date)
				}
			}
		}
		sort.Strings(dates)
		for _, date := range dates {
			content, readErr := os.ReadFile(filepath.Join(archiveDir, date+".md"))
			if readErr != nil {
				continue
			}
			if len(content) > 0 {
				parts = append(parts, string(content))
			}
		}
	}

	// Read active file
	data, err := os.ReadFile(memPath)
	if err == nil && len(data) > 0 {
		parts = append(parts, string(data))
	}

	return strings.Join(parts, "\n"), nil
}

// NeedsRotation returns true if the active memory file for a role was last
// modified before today (UTC). Returns false if the file doesn't exist.
func NeedsRotation(role string) bool {
	return needsRotationAt(MemoryPath(role))
}

// RotateMemory archives the active memory file to the per-role archive directory,
// using the file's modification date as the archive date. Also purges old archives.
func RotateMemory(role string, cfg RotationConfig) error {
	return rotateMemoryAt(MemoryPath(role), MemoryArchiveDir(role), cfg)
}

// PurgeOldArchives removes archive files older than RetentionDays.
func PurgeOldArchives(role string, cfg RotationConfig) error {
	return purgeOldArchivesAt(MemoryArchiveDir(role), cfg)
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

// allMemoryEntriesFromDir reads all memory files (active + archives) from a directory
// and tags them with the given source.
func allMemoryEntriesFromDir(dir, source string) []MemoryEntry {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var all []MemoryEntry
	for _, de := range dirEntries {
		if de.IsDir() {
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
				for i := range entries {
					entries[i].Source = source
				}
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
		for i := range entries {
			entries[i].Source = source
		}
		all = append(all, entries...)
	}

	return all
}

// AllMemoryEntriesWithArchives reads all memory files (active + archives)
// from both global and project memory, and returns their parsed entries.
func AllMemoryEntriesWithArchives() ([]MemoryEntry, error) {
	var all []MemoryEntry

	// Global memory
	globalEntries := allMemoryEntriesFromDir(GlobalMemoryDir(), "global")
	all = append(all, globalEntries...)

	// Project memory
	projectEntries := allMemoryEntriesFromDir(MemoryDir(), "project")
	all = append(all, projectEntries...)

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
