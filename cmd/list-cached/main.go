package main

import (
	"artifacts-proxy/pkg/cache"
	"artifacts-proxy/pkg/proxy"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	cacheDir := "cache"
	if len(os.Args) > 1 {
		cacheDir = os.Args[1]
	}

	matches, err := filepath.Glob(filepath.Join(cacheDir, "*.metadata"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	seen := make(map[string]struct{})
	var entries []string

	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", path, err)
			continue
		}
		var m cache.Metadata
		if err := json.Unmarshal(data, &m); err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", path, err)
			continue
		}

		fileType := "metadata"
		if proxy.IsArtifactURL(m.Params.URL) {
			fileType = "content"
		}

		key := m.Params.UpstreamName + "\t" + fileType + "\t" + m.Params.URL
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			entries = append(entries, key)
		}
	}

	sort.Strings(entries)
	for _, e := range entries {
		fmt.Println(e)
	}
}
