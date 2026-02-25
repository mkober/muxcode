package bus

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// CompactAlert describes a compaction recommendation for an agent role.
type CompactAlert struct {
	Role              string  `json:"role"`
	TotalBytes        int64   `json:"total_bytes"`
	MemoryBytes       int64   `json:"memory_bytes"`
	HistoryBytes      int64   `json:"history_bytes"`
	LogBytes          int64   `json:"log_bytes"`
	HoursSinceCompact float64 `json:"hours_since_compact"`
	Message           string  `json:"message"`
}

// CompactThresholds controls when compaction alerts fire.
// Both conditions must be met: total size > SizeBytes AND time since
// last compact > MinAge.
type CompactThresholds struct {
	SizeBytes int64
	MinAge    time.Duration
}

// DefaultCompactThresholds returns the default compaction thresholds:
// 512 KB total size and 2 hours since last compact.
func DefaultCompactThresholds() CompactThresholds {
	return CompactThresholds{
		SizeBytes: 512 * 1024,            // 512 KB
		MinAge:    2 * time.Hour,          // 2 hours
	}
}

// CheckCompaction checks all known roles for compaction recommendations.
func CheckCompaction(session string, th CompactThresholds) []CompactAlert {
	var alerts []CompactAlert
	for _, role := range KnownRoles {
		if alert := CheckRoleCompaction(session, role, th); alert != nil {
			alerts = append(alerts, *alert)
		}
	}
	return alerts
}

// CheckRoleCompaction checks a single role for compaction recommendation.
// Returns nil if compaction is not recommended (below thresholds or recently compacted).
func CheckRoleCompaction(session, role string, th CompactThresholds) *CompactAlert {
	// Measure file sizes (active + archives)
	memoryBytes := fileSize(MemoryPath(role)) + ArchiveTotalSize(role)
	historyBytes := fileSize(HistoryPath(session, role))
	logBytes := fileSize(LogPath(session))
	totalBytes := memoryBytes + historyBytes + logBytes

	// Check size threshold
	if totalBytes < th.SizeBytes {
		return nil
	}

	// Check time since last compact
	hoursSince := hoursSinceLastCompact(session, role)
	if hoursSince < th.MinAge.Hours() {
		return nil
	}

	return &CompactAlert{
		Role:              role,
		TotalBytes:        totalBytes,
		MemoryBytes:       memoryBytes,
		HistoryBytes:      historyBytes,
		LogBytes:          logBytes,
		HoursSinceCompact: hoursSince,
		Message:           formatCompactMessage(role, totalBytes, memoryBytes, historyBytes, logBytes, hoursSince),
	}
}

// FormatCompactAlert formats a compact alert as human-readable text.
func FormatCompactAlert(alert CompactAlert) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("\u26a0 COMPACT RECOMMENDED: %s\n", alert.Role))
	b.WriteString(fmt.Sprintf("  Total: %s  (memory: %s, history: %s, log: %s)\n",
		formatBytes(alert.TotalBytes),
		formatBytes(alert.MemoryBytes),
		formatBytes(alert.HistoryBytes),
		formatBytes(alert.LogBytes)))
	b.WriteString(fmt.Sprintf("  Last compact: %s ago\n", formatHours(alert.HoursSinceCompact)))
	b.WriteString("  Run: muxcode-agent-bus session compact \"<summary>\"\n")
	return b.String()
}

// CompactAlertKey returns a dedup key for a compact alert.
func CompactAlertKey(alert CompactAlert) string {
	return fmt.Sprintf("compact:%s", alert.Role)
}

// FilterNewCompactAlerts filters compact alerts that haven't been seen within cooldownSecs.
// Updates the lastSeen map with current timestamps for new alerts.
func FilterNewCompactAlerts(alerts []CompactAlert, lastSeen map[string]int64, cooldownSecs int64) []CompactAlert {
	now := time.Now().Unix()
	var fresh []CompactAlert
	for _, a := range alerts {
		key := CompactAlertKey(a)
		if ts, ok := lastSeen[key]; ok && (now-ts) < cooldownSecs {
			continue
		}
		lastSeen[key] = now
		fresh = append(fresh, a)
	}
	return fresh
}

// fileSize returns the size of a file in bytes, or 0 if the file doesn't exist.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// hoursSinceLastCompact returns the hours since the role's last compaction.
// Returns a large value if no compaction has ever occurred or if session meta
// cannot be read, ensuring the time threshold is met for never-compacted roles.
func hoursSinceLastCompact(session, role string) float64 {
	meta, err := ReadSessionMeta(session, role)
	if err != nil || meta == nil {
		// No session meta — treat as never compacted (large value)
		return 999.0
	}

	if meta.LastCompactTS == 0 {
		// Session exists but never compacted — use session start time
		elapsed := time.Since(time.Unix(meta.StartTS, 0))
		return elapsed.Hours()
	}

	elapsed := time.Since(time.Unix(meta.LastCompactTS, 0))
	return elapsed.Hours()
}

// formatCompactMessage builds the actionable alert message.
func formatCompactMessage(role string, total, memory, history, log int64, hours float64) string {
	return fmt.Sprintf(
		"Context approaching limits for %s (total: %s, memory: %s, history: %s, log: %s). Last compact: %s ago. Run: muxcode-agent-bus session compact \"<summary>\"",
		role,
		formatBytes(total),
		formatBytes(memory),
		formatBytes(history),
		formatBytes(log),
		formatHours(hours),
	)
}

// formatBytes formats a byte count as a human-readable string (KB/MB).
func formatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	kb := float64(b) / 1024
	if kb < 1024 {
		return fmt.Sprintf("%.0f KB", kb)
	}
	mb := kb / 1024
	return fmt.Sprintf("%.1f MB", mb)
}

// formatHours formats a fractional hour count as "Xh Ym".
func formatHours(h float64) string {
	totalMinutes := int(h * 60)
	hours := totalMinutes / 60
	minutes := totalMinutes % 60
	if hours == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, minutes)
}
