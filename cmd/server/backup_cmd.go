package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/cc-core/backup"
)

// defaultBackupPrefix namespaces this app's objects inside the shared bucket.
const defaultBackupPrefix = "cpa-claude/"

// runBackupCmd implements `<binary> backup` (and `backup keygen`). It snapshots
// the SQLite DB (if present), gathers the critical-file manifest, and ships an
// encrypted archive off-host. Exits non-zero on failure so the systemd oneshot
// reports it.
func runBackupCmd(args []string) {
	if len(args) > 0 && args[0] == "keygen" {
		pub, priv, err := backup.GenerateKeypair()
		if err != nil {
			fmt.Fprintf(os.Stderr, "keygen: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("# Backup keypair — put the PUBLIC key in config (backup.recipient_pubkey),\n")
		fmt.Printf("# and store the PRIVATE key OFFLINE (needed only for restore; never on the server).\n")
		fmt.Printf("public  (recipient_pubkey): %s\n", pub)
		fmt.Printf("private (KEEP OFFLINE):      %s\n", priv)
		return
	}

	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config file")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	if !cfg.Backup.Enabled {
		log.Info("backup: disabled (set backup.enabled: true to enable off-host backup) — nothing to do")
		return
	}

	if err := runBackup(cfg, *configPath); err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
}

// runBackup snapshots → archives → uploads. It returns errors instead of
// calling os.Exit so the deferred tmp-dir cleanup always runs (gocritic
// exitAfterDefer); runBackupCmd maps a non-nil error to a non-zero exit.
func runBackup(cfg *config.Config, configPath string) error {
	opt, err := backupOptions(cfg)
	if err != nil {
		return err
	}
	// System temp (not the config dir — that may be root-owned while the
	// backup runs as a less-privileged service user). PrivateTmp on the unit
	// keeps it isolated.
	tmpDir, err := os.MkdirTemp("", "cpa-claude-backup-")
	if err != nil {
		return fmt.Errorf("tmp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	entries, err := buildManifest(context.Background(), cfg, configPath, tmpDir)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	key, err := backup.RunBackup(context.Background(), opt, entries)
	if err != nil {
		return err
	}
	log.Infof("backup: uploaded %d files → s3://%s/%s", len(entries), opt.S3.Bucket, key)
	return nil
}

// runRestoreCmd implements `<binary> restore` for disaster recovery. The
// identity (private key) and read-only S3 credentials are supplied at the
// command line so they never need to live in a persisted config on the
// recovered host.
func runRestoreCmd(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config file (for S3 target)")
	date := fs.String("date", "latest", "backup date YYYY-MM-DD, or 'latest'")
	identityFile := fs.String("identity", "", "path to the offline private key file ('-' for stdin)")
	dest := fs.String("dest", "", "destination dir (default: config dir)")
	s3KeyID := fs.String("s3-access-key-id", "", "override S3 access key id (use the offline restore key)")
	s3Secret := fs.String("s3-secret-key", "", "override S3 secret key ('@/path' supported)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	s3, err := backupS3(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(*s3KeyID) != "" {
		s3.AccessKeyID = *s3KeyID
	}
	if strings.TrimSpace(*s3Secret) != "" {
		if v, err := loadKeyFile(*s3Secret); err == nil {
			s3.SecretAccessKey = v
		}
	}
	identity, err := readIdentity(*identityFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: identity: %v\n", err)
		os.Exit(1)
	}
	out := strings.TrimSpace(*dest)
	if out == "" {
		out = filepath.Dir(*configPath)
	}
	if err := backup.Restore(context.Background(), s3, identity, *date, out); err != nil {
		fmt.Fprintf(os.Stderr, "restore: %v\n", err)
		os.Exit(1)
	}
	log.Infof("restore: extracted backup (%s) → %s", *date, out)
}

func readIdentity(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("--identity is required (the offline private key)")
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func backupOptions(cfg *config.Config) (backup.Options, error) {
	s3, err := backupS3(cfg)
	if err != nil {
		return backup.Options{}, err
	}
	pub := strings.TrimSpace(cfg.Backup.RecipientPubKey)
	if pub == "" {
		return backup.Options{}, fmt.Errorf("backup.recipient_pubkey is empty (run `backup keygen`)")
	}
	return backup.Options{
		S3:              s3,
		RecipientPubKey: pub,
		RetentionDays:   cfg.Backup.RetentionDays,
	}, nil
}

func backupS3(cfg *config.Config) (backup.S3Config, error) {
	s := cfg.Backup.S3
	id, err := loadKeyFile(s.AccessKeyID)
	if err != nil {
		return backup.S3Config{}, fmt.Errorf("s3 access_key_id: %w", err)
	}
	secret, err := loadKeyFile(s.SecretAccessKey)
	if err != nil {
		return backup.S3Config{}, fmt.Errorf("s3 secret_access_key: %w", err)
	}
	prefix := strings.TrimSpace(s.Prefix)
	if prefix == "" {
		prefix = defaultBackupPrefix
	}
	return backup.S3Config{
		Endpoint:        s.Endpoint,
		Region:          s.Region,
		Bucket:          s.Bucket,
		Prefix:          prefix,
		AccessKeyID:     id,
		SecretAccessKey: secret,
	}, nil
}

// buildManifest collects every file that must survive a total server loss.
// saas.db is snapshotted (VACUUM INTO) for a consistent, WAL-free copy.
// Missing optional files are skipped silently; missing critical files
// (tokens.json) log a warning.
func buildManifest(ctx context.Context, cfg *config.Config, configPath, tmpDir string) ([]backup.FileEntry, error) {
	var entries []backup.FileEntry
	add := func(name, src string, mode os.FileMode) {
		if fileExists(src) {
			entries = append(entries, backup.FileEntry{Name: name, SourcePath: src, Mode: mode})
		}
	}

	// SQLite snapshot (wallet/orders, if the SaaS DB exists).
	if fileExists(cfg.SaaS.DBPath) {
		snap := filepath.Join(tmpDir, "saas.db")
		if err := backup.SnapshotSQLite(ctx, cfg.SaaS.DBPath, snap); err != nil {
			return nil, fmt.Errorf("snapshot saas.db: %w", err)
		}
		entries = append(entries, backup.FileEntry{Name: "saas.db", SourcePath: snap, Mode: 0o600})
		add("saas.db.jwt_secret", cfg.SaaS.DBPath+".jwt_secret", 0o600)
	}

	// Identity + config (the critical assets for CPA-Claude).
	tokensPath := filepath.Join(filepath.Dir(cfg.StateFile), "tokens.json")
	if fileExists(tokensPath) {
		entries = append(entries, backup.FileEntry{Name: "tokens.json", SourcePath: tokensPath, Mode: 0o600})
	} else {
		log.Warn("backup: tokens.json not present — skipping")
	}
	add("config.yaml", configPath, 0o600)

	// Upstream credentials (OAuth refresh_tokens — unrecoverable if lost).
	entries = append(entries, dirEntries(cfg.AuthDir, "auths")...)

	// In-config-dir secrets/ folder + best-effort state/monitor.
	configDir := filepath.Dir(configPath)
	entries = append(entries, dirEntries(filepath.Join(configDir, "secrets"), "secrets")...)
	add("state.json", cfg.StateFile, 0o600)
	add("monitor.json", filepath.Join(configDir, "monitor.json"), 0o600)

	if len(entries) == 0 {
		return nil, fmt.Errorf("nothing to back up")
	}
	return entries, nil
}

func dirEntries(dir, prefix string) []backup.FileEntry {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []backup.FileEntry
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		out = append(out, backup.FileEntry{
			Name:       prefix + "/" + e.Name(),
			SourcePath: filepath.Join(dir, e.Name()),
			Mode:       0o600,
		})
	}
	return out
}

func fileExists(p string) bool {
	if strings.TrimSpace(p) == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// loadKeyFile resolves either an inline secret or an "@/path" reference,
// keeping the file-loaded value out of the committed YAML.
func loadKeyFile(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "@") {
		data, err := os.ReadFile(s[1:])
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return s, nil
}
