// upload-talk-cliparts walks static/img/talks/ and uploads every PNG +
// AVIF to talks/<filename> in Spaces. Maintains a manifest at
// talks/_manifest.json mapping filename → sha256(content) so mediagen
// can fingerprint cliparts (for card-hash dedup) without keeping local
// copies on disk.
//
// Idempotent: skips files whose content hash matches the manifest.
// Use -force to re-upload + rewrite the manifest entry regardless.
// ConfTalk.Clipart in Notion stays untouched — the only change is
// *where* the bytes are served from.
//
// Usage:
//
//	go run ./cmd/upload-talk-cliparts            # upload changed
//	go run ./cmd/upload-talk-cliparts -force     # re-upload everything
//	go run ./cmd/upload-talk-cliparts -dry-run   # just list what'd happen
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"btcpp-web/external/spaces"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
)

const (
	configFile = "config.toml"
	srcDir     = "static/img/talks"
	keyPrefix  = "talks/"
)

type cfgFile struct {
	Spaces struct {
		Endpoint string `toml:"endpoint"`
		Region   string `toml:"region"`
		Bucket   string `toml:"bucket"`
		Key      string `toml:"key"`
		Secret   string `toml:"secret"`
	} `toml:"spaces"`
}

func main() {
	force := flag.Bool("force", false, "Re-upload even when the key already exists in Spaces")
	dryRun := flag.Bool("dry-run", false, "List what would be uploaded without actually uploading")
	flag.Parse()

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}

	spaces.Init(types.SpacesConfig{
		Endpoint: c.Spaces.Endpoint,
		Region:   c.Spaces.Region,
		Bucket:   c.Spaces.Bucket,
		Key:      c.Spaces.Key,
		Secret:   c.Spaces.Secret,
	})
	if !spaces.IsConfigured() {
		log.Fatalf("spaces not configured (check [spaces] in %s)", configFile)
	}

	manifest, err := spaces.LoadJSONMap(spaces.TalkManifestKey)
	if err != nil {
		log.Fatalf("load manifest: %s", err)
	}
	log.Printf("loaded manifest: %d entries", len(manifest))

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		log.Fatalf("read %s: %s", srcDir, err)
	}

	var uploaded, skippedSame, skippedExt, failed int
	manifestDirty := false
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		ext := strings.ToLower(filepath.Ext(name))
		ct := contentTypeFor(ext)
		if ct == "" {
			skippedExt++
			continue
		}
		key := keyPrefix + name

		body, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			log.Printf("read %s: %s", name, err)
			failed++
			continue
		}
		hash := contentHash(body)

		// Skip when the manifest already records this exact hash —
		// the bytes haven't changed since the last upload, so no
		// network round-trip needed. Also covers the spaces.Exists
		// case (a file whose hash matches must be in Spaces).
		if !*force && manifest[name] == hash {
			skippedSame++
			continue
		}

		if *dryRun {
			log.Printf("dry: would upload %s → %s (%s, %s)", name, key, ct, hash)
			uploaded++
			manifest[name] = hash
			manifestDirty = true
			continue
		}

		if _, err := spaces.Upload(key, body, ct, ""); err != nil {
			log.Printf("upload %s: %s", key, err)
			failed++
			continue
		}
		log.Printf("uploaded %s (%d bytes, %s)", key, len(body), hash)
		uploaded++
		manifest[name] = hash
		manifestDirty = true
	}

	// Re-upload the manifest only if any entry changed. Skip when
	// nothing was uploaded — saves an unnecessary write.
	if manifestDirty && !*dryRun {
		if err := spaces.SaveJSONMap(spaces.TalkManifestKey, manifest); err != nil {
			log.Printf("save manifest: %s", err)
			failed++
		} else {
			log.Printf("manifest updated: %d entries → %s", len(manifest), spaces.TalkManifestKey)
		}
	}

	log.Printf("done: uploaded=%d skipped-unchanged=%d skipped-non-image=%d failed=%d (force=%t dry-run=%t)",
		uploaded, skippedSame, skippedExt, failed, *force, *dryRun)
}

// contentHash returns the sha256 of `data` as a 16-char hex prefix.
// Same shape as mediagen's other card hashes so a manifest-stored
// fingerprint is a drop-in fingerprint for the card-cache hash chain.
func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:])[:16]
}

// contentTypeFor returns the MIME type for a file extension we want to
// upload, or "" for extensions to skip. Talk cliparts are png/avif
// pairs; anything else (e.g. an .DS_Store sneaking in) gets skipped.
func contentTypeFor(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".avif":
		return "image/avif"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}
