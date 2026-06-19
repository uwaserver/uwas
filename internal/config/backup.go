package config

// BackupConfig controls automated backup behavior.
type BackupConfig struct {
	Enabled      bool              `yaml:"enabled"`
	Provider     string            `yaml:"provider"`       // local | s3 | sftp
	Schedule     string            `yaml:"schedule"`       // duration string e.g. "24h" (fallback if Cron is empty)
	Cron         string            `yaml:"cron"`           // cron expression e.g. "0 2 * * *" (5-field, local timezone)
	Keep         int               `yaml:"keep"`           // keep last N backups
	MaxFileSize  int64             `yaml:"max_file_size"`  // max bytes per file (default 500MB)
	MaxTotalSize int64             `yaml:"max_total_size"` // max bytes total (default 10GB)
	Local        BackupLocalConfig `yaml:"local"`
	S3           BackupS3Config    `yaml:"s3"`
	SFTP         BackupSFTPConfig  `yaml:"sftp"`
}

// BackupLocalConfig is the local-filesystem backup provider.
type BackupLocalConfig struct {
	Path string `yaml:"path"`
}

// BackupS3Config is the S3-compatible backup provider.
type BackupS3Config struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Region    string `yaml:"region"`
}

// BackupSFTPConfig is the SFTP backup provider.
type BackupSFTPConfig struct {
	Host               string `yaml:"host"`
	Port               int    `yaml:"port"`
	User               string `yaml:"user"`
	KeyFile            string `yaml:"key_file"`
	Password           string `yaml:"password"`
	RemotePath         string `yaml:"remote_path"`
	InsecureKnownHosts bool   `yaml:"insecure_known_hosts"` // Allow unknown hosts (auto-accept TOFU — not recommended for production)
}
