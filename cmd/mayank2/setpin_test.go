package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"mayank2/internal/db"
)

func TestParseSetPinFlags_rejectsPINAsArgument(t *testing.T) {
	// A bare positional argument must be refused — the whole point of this
	// ticket is that the PIN never travels through argv / shell history.
	if _, _, _, err := parseSetPinFlags([]string{"1234"}); err == nil {
		t.Fatal("parseSetPinFlags accepted a positional PIN argument; it must not")
	}
}

func TestParseSetPinFlags_help(t *testing.T) {
	_, _, _, err := parseSetPinFlags([]string{"-h"})
	if !errors.Is(err, errHelp) {
		t.Fatalf("got %v, want errHelp", err)
	}
}

func TestParseSetPinFlags_dbFlag(t *testing.T) {
	_, _, dbPath, err := parseSetPinFlags([]string{"-db", `C:\tmp\t.db`})
	if err != nil {
		t.Fatalf("parseSetPinFlags: %v", err)
	}
	if dbPath != `C:\tmp\t.db` {
		t.Fatalf("dbPath=%q", dbPath)
	}
}

func TestReadPIN_nonTerminalReadsOneLine(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	if _, err := w.WriteString("4242\n"); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	w.Close()

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	defer devnull.Close()

	pin, err := readPIN(r, devnull)
	if err != nil {
		t.Fatalf("readPIN: %v", err)
	}
	if pin != "4242" {
		t.Fatalf("pin=%q, want 4242", pin)
	}
}

func TestCmdSetPin_storesRealBcryptHash_notPlaintext(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	envPath := filepath.Join(dir, ".env") // does not need to exist

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if _, err := w.WriteString("9999\n"); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	w.Close()

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	code := cmdSetPin(context.Background(), []string{"-db", dbPath, "-env", envPath})
	if code != 0 {
		t.Fatalf("cmdSetPin exit=%d, want 0", code)
	}

	sqlDB, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	var stored string
	err = sqlDB.QueryRowContext(context.Background(),
		`SELECT value FROM settings WHERE key='pin_hash'`).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatal("set-pin did not write a pin_hash row")
	}
	if err != nil {
		t.Fatalf("read pin_hash: %v", err)
	}
	if stored == "9999" {
		t.Fatal("set-pin stored the raw PIN, not a bcrypt hash")
	}
	if bcrypt.CompareHashAndPassword([]byte(stored), []byte("9999")) != nil {
		t.Fatalf("stored pin_hash does not bcrypt-verify against the entered PIN: %q", stored)
	}
	if bcrypt.CompareHashAndPassword([]byte(stored), []byte("0000")) == nil {
		t.Fatal("stored pin_hash incorrectly verifies a wrong PIN")
	}
}

func TestCmdSetPin_rejectsEmptyPIN(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	envPath := filepath.Join(dir, ".env")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if _, err := w.WriteString("\n"); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	w.Close()

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	code := cmdSetPin(context.Background(), []string{"-db", dbPath, "-env", envPath})
	if code == 0 {
		t.Fatal("cmdSetPin accepted an empty PIN")
	}
}
