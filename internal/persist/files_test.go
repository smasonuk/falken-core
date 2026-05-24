package persist_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/smasonuk/falken-core/internal/persist"
)

func TestWriteTextAtomicCreatesParentDirectoriesAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "plan.txt")

	if err := persist.WriteTextAtomic(path, "hello plan", 0o600); err != nil {
		t.Fatalf("WriteTextAtomic: %v", err)
	}

	got, found, err := persist.ReadText(path)
	if err != nil {
		t.Fatalf("ReadText: %v", err)
	}
	if !found {
		t.Fatal("expected written text file to exist")
	}
	if got != "hello plan" {
		t.Fatalf("text content = %q, want %q", got, "hello plan")
	}
}

func TestWriteBytesAtomicPreservesBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "data.bin")
	want := []byte{0xff, 0x00, 'a', '\r', '\n', 0xfe}

	if err := persist.WriteBytesAtomic(path, want, 0o600); err != nil {
		t.Fatalf("WriteBytesAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bytes = %#v, want %#v", got, want)
	}
}

func TestReadTextMissingReturnsDeterministicEmptyResult(t *testing.T) {
	got, found, err := persist.ReadText(filepath.Join(t.TempDir(), "missing.txt"))
	if err != nil {
		t.Fatalf("ReadText: %v", err)
	}
	if found {
		t.Fatal("expected missing text file to report found=false")
	}
	if got != "" {
		t.Fatalf("text content = %q, want empty string", got)
	}
}

func TestWriteJSONAtomicCreatesParentDirectoriesAndRoundTrips(t *testing.T) {
	type payload struct {
		Name  string   `json:"name"`
		Items []string `json:"items"`
	}

	path := filepath.Join(t.TempDir(), "nested", "state.json")
	want := payload{
		Name:  "memory",
		Items: []string{"one", "two"},
	}

	if err := persist.WriteJSONAtomic(path, want, 0o600); err != nil {
		t.Fatalf("WriteJSONAtomic: %v", err)
	}

	var got payload
	found, err := persist.ReadJSON(path, &got)
	if err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if !found {
		t.Fatal("expected written JSON file to exist")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON payload = %+v, want %+v", got, want)
	}
}

func TestReadJSONMissingReturnsDeterministicEmptyResult(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	var got payload
	found, err := persist.ReadJSON(filepath.Join(t.TempDir(), "missing.json"), &got)
	if err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if found {
		t.Fatal("expected missing JSON file to report found=false")
	}
	if got != (payload{}) {
		t.Fatalf("JSON payload = %+v, want zero value", got)
	}
}
