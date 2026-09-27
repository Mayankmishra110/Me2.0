package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mayank2/internal/config"
	"mayank2/internal/db"
)

func cmdMigrate(ctx context.Context, args []string) int {
	configPath, envPath, dbPath, rest, err := parseMigrateFlags(args)
	if err == errHelp {
		fmt.Fprint(os.Stderr, usage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "migrate: unexpected args %v\n", rest)
		return 2
	}

	if err := config.LoadEnvFile(envPath); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: load env: %v\n", err)
		return 1
	}

	if dbPath == "" {
		cfgPath := configPath
		if !fileExists(cfgPath) {
			example := filepath.Join(filepath.Dir(cfgPath), "config.example.yaml")
			if fileExists(example) {
				cfgPath = example
			}
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "migrate: load config: %v\n", err)
			return 1
		}
		dbPath = db.DefaultPath(cfg.DataDir)
	}

	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: open db: %v\n", err)
		return 1
	}
	defer sqlDB.Close()

	applied, err := db.Migrate(ctx, sqlDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		return 1
	}
	if len(applied) == 0 {
		fmt.Printf("migrate: already up to date (%s)\n", dbPath)
		return 0
	}
	fmt.Printf("migrate: applied %s → %s\n", strings.Join(applied, ", "), dbPath)
	return 0
}

func parseMigrateFlags(args []string) (configPath, envPath, dbPath string, rest []string, err error) {
	configPath, envPath, _ = defaultPaths()
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-config" || a == "--config":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			configPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-config="):
			configPath = strings.TrimPrefix(a, "-config=")
			i++
		case a == "-env" || a == "--env":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			envPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-env="):
			envPath = strings.TrimPrefix(a, "-env=")
			i++
		case a == "-db" || a == "--db":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			dbPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-db="):
			dbPath = strings.TrimPrefix(a, "-db=")
			i++
		case a == "-h" || a == "--help":
			return "", "", "", nil, errHelp
		default:
			rest = append(rest, a)
			i++
		}
	}
	return configPath, envPath, dbPath, rest, nil
}
