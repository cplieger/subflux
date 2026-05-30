package config

import "time"

// BackupEnabled reports whether scheduled database backups are enabled.
func (c *Config) BackupEnabled() bool { return c.Backup.Enabled }

// BackupPath returns the backup directory override. Empty means backups are
// written next to the database file.
func (c *Config) BackupPath() string { return c.Backup.Path }

// BackupFrequency returns how often a backup is taken.
func (c *Config) BackupFrequency() time.Duration { return c.Backup.Frequency.D }

// BackupRetention returns how many backup files to keep on disk.
func (c *Config) BackupRetention() int { return c.Backup.Retention }
