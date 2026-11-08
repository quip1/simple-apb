// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build ignore

// gen copies the "mail.*" strings from the shared translation catalogs
// (/i18n/*.json, also used by the web app) into catalog/, so the server can
// embed them. Run with `go generate ./...` from server/.
package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "i18n", "*.json"))
	if err != nil || len(files) == 0 {
		log.Fatalf("no catalogs found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		var all map[string]string
		if err := json.Unmarshal(raw, &all); err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		keys := []string{}
		for k := range all {
			if strings.HasPrefix(k, "mail.") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		out := map[string]string{}
		for _, k := range keys {
			out[k] = all[k]
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		dest := filepath.Join("catalog", filepath.Base(f))
		if err := os.WriteFile(dest, append(data, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
