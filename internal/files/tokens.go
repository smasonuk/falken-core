package files

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// ReadToken records that a caller read a specific version of a canonical file.
type ReadToken struct {
	ID          string
	ScopeID     string
	Path        string
	ContentHash string
	Size        int64
	ModTime     time.Time
	Device      uint64
	Inode       uint64
	IssuedAt    time.Time
}

// TokenValidation compares a registered read token with the file's current state.
type TokenValidation struct {
	Found   bool
	Matches bool
	Token   ReadToken
	Current ReadToken
	Reason  string
}

// TokenRegistry stores run-scoped read tokens in memory.
type TokenRegistry struct {
	mu      sync.Mutex
	scopeID string
	tokens  map[string]ReadToken
}

// NewTokenRegistry creates an in-memory read-token registry for a run/session scope.
func NewTokenRegistry(scopeID string) *TokenRegistry {
	if scopeID == "" {
		scopeID = "default"
	}

	return &TokenRegistry{
		scopeID: scopeID,
		tokens:  make(map[string]ReadToken),
	}
}

// ScopeID returns the registry scope label recorded on issued tokens.
func (r *TokenRegistry) ScopeID() string {
	if r == nil {
		return ""
	}
	return r.scopeID
}

func (r *TokenRegistry) record(token ReadToken) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tokens[token.Path] = token
}

func (r *TokenRegistry) forget(path string) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.tokens, filepath.Clean(path))
}

// Lookup returns the most recent read token for a canonical file path.
func (r *TokenRegistry) Lookup(path string) (ReadToken, bool) {
	if r == nil {
		return ReadToken{}, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	token, ok := r.tokens[filepath.Clean(path)]
	return token, ok
}

// ValidateCurrent compares the current file state with the registered token.
func (r *TokenRegistry) ValidateCurrent(path string) (TokenValidation, error) {
	token, found := r.Lookup(path)
	if !found {
		return TokenValidation{
			Found:  false,
			Reason: "no read token is registered for path",
		}, nil
	}

	current, err := tokenForFile(token.ScopeID, token.Path, time.Time{})
	if err != nil {
		return TokenValidation{}, err
	}

	matches := sameFileVersion(token, current)
	reason := "current file matches read token"
	if !matches {
		reason = "current file differs from read token"
	}

	return TokenValidation{
		Found:   true,
		Matches: matches,
		Token:   token,
		Current: current,
		Reason:  reason,
	}, nil
}

func tokenForFile(scopeID, path string, issuedAt time.Time) (ReadToken, error) {
	cleanPath := filepath.Clean(path)
	data, stat, err := readManagedExistingFileSnapshot(cleanPath, filepath.Dir(cleanPath))
	if err != nil {
		return ReadToken{}, fmt.Errorf("read token file snapshot: %w", err)
	}
	return tokenForSnapshot(scopeID, cleanPath, data, stat, issuedAt), nil
}

func tokenForSnapshot(scopeID, path string, data []byte, stat os.FileInfo, issuedAt time.Time) ReadToken {
	cleanPath := filepath.Clean(path)
	hash := sha256.Sum256(data)
	contentHash := hex.EncodeToString(hash[:])
	modTime := stat.ModTime().UTC()
	if issuedAt.IsZero() {
		issuedAt = time.Now().UTC()
	}

	device, inode := fileIdentity(stat)
	return ReadToken{
		ID:          tokenID(scopeID, cleanPath, contentHash, stat.Size(), modTime, device, inode),
		ScopeID:     scopeID,
		Path:        cleanPath,
		ContentHash: contentHash,
		Size:        stat.Size(),
		ModTime:     modTime,
		Device:      device,
		Inode:       inode,
		IssuedAt:    issuedAt,
	}
}

func tokenID(scopeID, path, contentHash string, size int64, modTime time.Time, device, inode uint64) string {
	seed := scopeID + "\x00" + path + "\x00" + contentHash + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(modTime.UnixNano(), 10) + "\x00" + strconv.FormatUint(device, 10) + "\x00" + strconv.FormatUint(inode, 10)
	hash := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(hash[:])
}

func sameFileVersion(a, b ReadToken) bool {
	return a.Path == b.Path &&
		a.ContentHash == b.ContentHash &&
		a.Size == b.Size &&
		a.ModTime.Equal(b.ModTime) &&
		a.Device == b.Device &&
		a.Inode == b.Inode
}
