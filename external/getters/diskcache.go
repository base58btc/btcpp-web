package getters

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const cacheDir = "_cache"

var diskCacheEnabled = false

func EnableDiskCache() {
	os.MkdirAll(cacheDir, 0755)
	diskCacheEnabled = true
}

func cachePath(name string) string {
	return filepath.Join(cacheDir, name+".json")
}

func writeCache(name string, data interface{}) {
	if !diskCacheEnabled {
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	os.WriteFile(cachePath(name), b, 0644)
}

func readCache(name string, dest interface{}) bool {
	if !diskCacheEnabled {
		return false
	}
	b, err := os.ReadFile(cachePath(name))
	if err != nil {
		return false
	}
	return json.Unmarshal(b, dest) == nil
}
