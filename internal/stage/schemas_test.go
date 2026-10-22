package stage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/masteine/relay/internal/protocol"
)

func TestSchemaPerStage(t *testing.T) {
	for _, s := range []protocol.Stage{protocol.StageDiscuss, protocol.StageTicket, protocol.StageTriage, protocol.StagePlan} {
		got, err := Schema(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if strings.Contains(got, "$schema") || strings.Contains(got, "\n") {
			t.Errorf("%s: schema must be single-line and without $schema: %s", s, got)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(got), &m); err != nil {
			t.Errorf("%s: not JSON: %v", s, err)
		}
		if m["type"] != "object" || m["required"] == nil {
			t.Errorf("%s: schema should be an object with required fields", s)
		}
	}
	if _, err := Schema(protocol.StageApprove); err == nil {
		t.Error("approve has no schema and must say so")
	}
}

// TestSchemasMatchPlugin keeps plugin/schemas/ identical to the embedded
// copies, so that what the reader sees is what the binary validates.
func TestSchemasMatchPlugin(t *testing.T) {
	entries, err := schemaFS.ReadDir("schemas")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded schemas")
	}
	for _, e := range entries {
		embedded, _ := schemaFS.ReadFile("schemas/" + e.Name())
		shipped, err := os.ReadFile(filepath.Join("..", "..", "plugin", "schemas", e.Name()))
		if err != nil {
			t.Errorf("%s: missing in plugin/schemas: %v", e.Name(), err)
			continue
		}
		if !bytes.Equal(embedded, shipped) {
			t.Errorf("%s differs between internal/stage/schemas and plugin/schemas", e.Name())
		}
	}
}
