package stage

import (
	"embed"
	"encoding/json"
	"fmt"

	"github.com/masteine/relay/internal/protocol"
)

// schemaFS holds the JSON Schemas Claude's structured output is validated
// against, one per stage. plugin/schemas/ carries identical copies for the
// reader; a test keeps the two in sync.
//
//go:embed schemas/*.json
var schemaFS embed.FS

// Schema returns the schema for a stage as the single-line JSON that
// --json-schema takes. A "$schema" key is dropped: the CLI's validator
// rejects meta-schema references (spike M2).
func Schema(stage protocol.Stage) (string, error) {
	raw, err := schemaFS.ReadFile("schemas/" + string(stage) + ".json")
	if err != nil {
		return "", fmt.Errorf("no output schema for stage %q", stage)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("schema %s: %w", stage, err)
	}
	delete(m, "$schema")
	out, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
