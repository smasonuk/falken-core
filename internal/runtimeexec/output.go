package runtimeexec

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultMaxInlineOutput is the default byte limit for command output returned inline.
	DefaultMaxInlineOutput = 64 * 1024
)

var (
	// ErrOutputArtifactRootRequired indicates oversized output could not be persisted.
	ErrOutputArtifactRootRequired = errors.New("output artifact root is required for truncated output")

	artifactSequence atomic.Uint64
)

// OutputSummary reports truncation and artifact details for captured command output.
type OutputSummary struct {
	Truncated                   bool
	ArtifactPath                string
	ArtifactWorkspaceAccessible bool
	InlineLimit                 int
	OriginalBytes               int
	PreviewBytes                int
}

type capturedOutput struct {
	stdout   string
	stderr   string
	combined string
	summary  OutputSummary
}

func commandOutputArtifactPath(root string) string {
	timestamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	sequence := artifactSequence.Add(1)
	name := fmt.Sprintf("command-output-%s-%06d.txt", timestamp, sequence)
	return filepath.Join(root, name)
}

type commandOutputCollectors struct {
	stdout   *outputCollector
	stderr   *outputCollector
	combined *outputCollector
}

func newCommandOutputCollectors(state *ExecutionState, limit int) commandOutputCollectors {
	limit = normalizeOutputLimit(limit)
	return commandOutputCollectors{
		stdout:   newOutputCollector(limit, "", false),
		stderr:   newOutputCollector(limit, "", false),
		combined: newOutputCollector(limit, state.OutputArtifactRoot(), true, !state.VirtualWorkspace()),
	}
}

func (c commandOutputCollectors) result() (capturedOutput, error) {
	stdout, _, stdoutErr := c.stdout.snapshot()
	stderr, _, stderrErr := c.stderr.snapshot()
	combined, summary, combinedErr := c.combined.snapshot()
	if err := errors.Join(stdoutErr, stderrErr, combinedErr); err != nil {
		return capturedOutput{}, err
	}
	return capturedOutput{
		stdout:   stdout,
		stderr:   stderr,
		combined: combined,
		summary:  summary,
	}, nil
}

func normalizeOutputLimit(limit int) int {
	if limit <= 0 {
		return DefaultMaxInlineOutput
	}
	return limit
}

type outputCollector struct {
	mu                  sync.Mutex
	inline              bytes.Buffer
	limit               int
	total               int
	spool               bool
	artifactRoot        string
	artifactPath        string
	workspaceAccessible bool
	artifactFile        *os.File
	artifactErr         error
	closed              bool
}

func newOutputCollector(limit int, artifactRoot string, spool bool, workspaceAccessible ...bool) *outputCollector {
	accessible := false
	if len(workspaceAccessible) > 0 {
		accessible = workspaceAccessible[0]
	}
	return &outputCollector{
		limit:               normalizeOutputLimit(limit),
		spool:               spool,
		artifactRoot:        artifactRoot,
		workspaceAccessible: accessible,
	}
}

func (c *outputCollector) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	previousTotal := c.total
	c.total += len(data)

	if previousTotal < c.limit {
		inlineBytes := min(len(data), c.limit-previousTotal)
		if inlineBytes > 0 {
			_, _ = c.inline.Write(data[:inlineBytes])
		}
	}

	if c.spool && c.total > c.limit {
		overflowOffset := 0
		if previousTotal < c.limit {
			overflowOffset = c.limit - previousTotal
		}
		if err := c.ensureArtifactLocked(); err != nil {
			c.artifactErr = err
			return len(data), nil
		}
		if overflowOffset < len(data) {
			if _, err := c.artifactFile.Write(data[overflowOffset:]); err != nil {
				c.artifactErr = fmt.Errorf("write truncated output artifact: %w", err)
			}
		}
	}

	return len(data), nil
}

func (c *outputCollector) snapshot() (string, OutputSummary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.artifactFile != nil && !c.closed {
		if err := c.artifactFile.Close(); err != nil && c.artifactErr == nil {
			c.artifactErr = fmt.Errorf("close truncated output artifact: %w", err)
		}
		c.closed = true
	}

	summary := OutputSummary{
		Truncated:                   c.total > c.limit,
		ArtifactPath:                c.artifactPath,
		ArtifactWorkspaceAccessible: c.artifactPath != "" && c.workspaceAccessible,
		InlineLimit:                 c.limit,
		OriginalBytes:               c.total,
		PreviewBytes:                c.inline.Len(),
	}
	if c.artifactErr != nil {
		return "", OutputSummary{}, c.artifactErr
	}
	return c.inline.String(), summary, nil
}

func (c *outputCollector) ensureArtifactLocked() error {
	if c.artifactFile != nil {
		return nil
	}
	if c.artifactErr != nil {
		return c.artifactErr
	}
	if c.artifactRoot == "" {
		return ErrOutputArtifactRootRequired
	}
	if err := os.MkdirAll(c.artifactRoot, 0o755); err != nil {
		return fmt.Errorf("create truncated output artifact directory: %w", err)
	}
	artifactPath := commandOutputArtifactPath(c.artifactRoot)
	file, err := os.OpenFile(artifactPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create truncated output artifact: %w", err)
	}
	if _, err := file.Write(c.inline.Bytes()); err != nil {
		_ = file.Close()
		return fmt.Errorf("write truncated output artifact: %w", err)
	}
	c.artifactPath = artifactPath
	c.artifactFile = file
	return nil
}
