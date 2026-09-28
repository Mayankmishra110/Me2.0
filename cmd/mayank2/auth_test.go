package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAuthFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantPlat string
		wantAcct string
		wantDB   string
		wantErr  bool
		wantHelp bool
	}{
		{
			name:     "positional",
			args:     []string{"youtube", "money-en"},
			wantPlat: "youtube",
			wantAcct: "money-en",
		},
		{
			name:     "with-db",
			args:     []string{"-db", `C:\tmp\t.db`, "meta", "page1"},
			wantPlat: "meta",
			wantAcct: "page1",
			wantDB:   `C:\tmp\t.db`,
		},
		{
			name:    "missing-account",
			args:    []string{"youtube"},
			wantErr: true,
		},
		{
			name:     "help",
			args:     []string{"-h"},
			wantHelp: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, dbPath, plat, acct, err := parseAuthFlags(tc.args)
			if tc.wantHelp {
				if !errors.Is(err, errHelp) {
					t.Fatalf("got %v want errHelp", err)
				}
				return
			}
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAuthFlags: %v", err)
			}
			if plat != tc.wantPlat || acct != tc.wantAcct {
				t.Fatalf("got %s/%s want %s/%s", plat, acct, tc.wantPlat, tc.wantAcct)
			}
			if tc.wantDB != "" && dbPath != tc.wantDB {
				t.Fatalf("dbPath=%q want %q", dbPath, tc.wantDB)
			}
		})
	}
}

func TestAuthUsageListsPlatforms(t *testing.T) {
	for _, p := range []string{"youtube", "meta", "x", "pinterest", "linkedin"} {
		if !strings.Contains(authUsage, p) {
			t.Fatalf("authUsage missing %q", p)
		}
	}
}
