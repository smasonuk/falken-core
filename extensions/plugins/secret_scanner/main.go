package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

type PluginPayload struct {
	Hook    string         `json:"hook"`
	Args    map[string]any `json:"args"`
	RealCWD string         `json:"cwd"`
}

var keepAlive []byte

//export alloc_mem
func alloc_mem(size uint32) uint32 {
	keepAlive = make([]byte, size+1)
	return uint32(uintptr(unsafe.Pointer(&keepAlive[0])))
}

func main() {
	inputBytes, _ := io.ReadAll(os.Stdin)

	var payload PluginPayload
	if err := json.Unmarshal(inputBytes, &payload); err != nil {
		fmt.Printf(`{"error": "failed to parse JSON payload"}` + "\n")
		return
	}

	if payload.Hook == "scan_workspace" {
		suspiciousFiles := scanDirectory(payload.RealCWD)
		outBytes, _ := json.Marshal(map[string]any{"suspicious_files": suspiciousFiles})
		fmt.Print(string(outBytes))
		return
	}

	fmt.Printf(`{"error": "unknown hook"}` + "\n")
}

func scanDirectory(root string) []string {
	if root == "" {
		root = "."
	}

	var suspicious []string
	ignoreDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		"vendor":       true,
		"bin":          true,
		"build":        true,
	}

	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			relPath = path
		}

		// Check filename/extension rules
		name := strings.ToLower(d.Name())
		ext := filepath.Ext(name)
		if ext == ".pem" || ext == ".key" || ext == ".p12" || name == "id_rsa" {
			suspicious = append(suspicious, relPath)
			return nil
		}

		if strings.HasPrefix(name, ".env") || name == "secrets.json" || name == "credentials.json" {
			suspicious = append(suspicious, relPath)
			return nil
		}

		// Deep inspect file contents (read only the first 2KB for speed)
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		buf := make([]byte, 2048)
		n, _ := f.Read(buf)
		if n > 0 {
			content := buf[:n]
			// Skip binary files (simple check: looking for null bytes)
			if bytes.IndexByte(content, 0) != -1 {
				return nil
			}

			for _, re := range pluginsdk.SecretPatterns {
				if re.Match(content) {
					suspicious = append(suspicious, relPath)
					return nil // Found a secret, move to the next file
				}
			}
		}

		return nil
	})

	return suspicious
}
