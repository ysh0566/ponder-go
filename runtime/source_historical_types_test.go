package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type historicalTypeFile struct {
	File string `json:"file"`
	SHA  string `json:"sha256"`
}
type historicalTypeBuild struct {
	Exit   int    `json:"exit_code"`
	Stderr string `json:"stderr"`
}
type historicalTypeControl struct {
	SourceID       string `json:"source_id"`
	SourceFile     string `json:"source_file"`
	SourceSHA      string `json:"source_sha256"`
	Body           string `json:"source_body"`
	Index          int    `json:"directive_index"`
	Kind, Role     string
	PositiveSource historicalTypeFile  `json:"positive_source"`
	NegativeSource historicalTypeFile  `json:"negative_source"`
	Binary         historicalTypeFile  `json:"positive_binary"`
	PositiveBuild  historicalTypeBuild `json:"build_positive"`
	NegativeBuild  historicalTypeBuild `json:"build_negative"`
	Diagnostic     bool                `json:"specific_negative_diagnostic"`
	Status         string
}

func historicalTypeVerifyFile(root string, file historicalTypeFile) error {
	raw, err := os.ReadFile(filepath.Join(root, file.File))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != file.SHA {
		return fmt.Errorf("historical type artifact changed: %s", file.File)
	}
	return nil
}
func historicalLoadTypes(root string, records []historicalLiveRecord) ([]historicalTypeControl, error) {
	var fixture struct {
		Complete, Success bool
		Directives        []historicalTypeControl
		Production        []historicalTypeFile `json:"production_inputs"`
	}
	if err := historicalLiveReadJSON(root+"/testdata/runtime/source-historical-types.json", &fixture); err != nil {
		return nil, err
	}
	if !fixture.Complete || !fixture.Success || len(fixture.Directives) != 12 || len(fixture.Production) < 100 {
		return nil, fmt.Errorf("all12actual historical compiler controls required")
	}
	for _, file := range fixture.Production {
		if err := historicalTypeVerifyFile(root, file); err != nil {
			return nil, err
		}
	}
	inventory := map[string]historicalLiveRecord{}
	for _, record := range records {
		inventory[record.ID] = record
	}
	seen := map[string]bool{}
	for _, control := range fixture.Directives {
		record, ok := inventory[control.SourceID]
		key := fmt.Sprintf("%s/%d", control.SourceID, control.Index)
		if !ok || seen[key] || control.SourceSHA != record.SourceSHA || control.Body != record.Body || control.SourceFile != record.File || control.Index < 0 || control.Index >= len(record.TypeDirectives) {
			return nil, fmt.Errorf("historical compiler control differs from exact source annotation")
		}
		seen[key] = true
		if control.PositiveBuild.Exit != 0 || control.NegativeBuild.Exit <= 0 || !control.Diagnostic || control.Status != "runtime_pending" {
			return nil, fmt.Errorf("historical positive/negative actual builds missing")
		}
		if control.Role == "return" {
			if !strings.Contains(control.NegativeBuild.Stderr, "cannot use") || !strings.Contains(control.NegativeBuild.Stderr, "SourceLog") || !strings.Contains(control.NegativeBuild.Stderr, "any") {
				return nil, fmt.Errorf("historical override requires actual returntype diagnostic")
			}
		} else if !strings.Contains(control.NegativeBuild.Stderr, "has no field or method Address") && !strings.Contains(control.NegativeBuild.Stderr, "has no field or method SetAddress") {
			return nil, fmt.Errorf("historical property requires actual closed-filter diagnostic")
		}
		for _, file := range []historicalTypeFile{control.PositiveSource, control.NegativeSource, control.Binary} {
			if !strings.HasPrefix(file.File, ".parity-build/source-historical-types/") {
				return nil, fmt.Errorf("historical type build artifact escaped task build directory")
			}
			if err := historicalTypeVerifyFile(root, file); err != nil {
				return nil, err
			}
		}
	}
	for _, record := range records {
		for index := range record.TypeDirectives {
			if !seen[fmt.Sprintf("%s/%d", record.ID, index)] {
				return nil, fmt.Errorf("historical annotation missing")
			}
		}
	}
	return fixture.Directives, nil
}
func historicalExecuteType(ctx context.Context, root, dialect string, control historicalTypeControl, payload json.RawMessage) (map[string]any, error) {
	if err := historicalTypeVerifyFile(root, control.Binary); err != nil {
		return nil, err
	}
	var input struct {
		CaseID  string `json:"case_id"`
		Backend string `json:"backend"`
	}
	if err := json.Unmarshal(payload, &input); err != nil {
		return nil, err
	}
	if input.CaseID != control.SourceID || input.Backend != dialect {
		return nil, fmt.Errorf("exact original type payload identity required")
	}
	cmd := exec.CommandContext(ctx, filepath.Join(root, control.Binary.File))
	cmd.Dir = root
	cmd.Env = os.Environ()
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	result := map[string]any{"source_id": control.SourceID, "directive_index": control.Index, "backend": dialect, "positive_binary_sha256": control.Binary.SHA, "stdout": stdout.String(), "stderr": stderr.String(), "exit_error": fmt.Sprint(runErr), "passed": false, "native_backend_closed": true, "phase": "after_original_case_comparisons_before_original_setupAnvil_cleanup"}
	if runErr != nil {
		return result, fmt.Errorf("historical typepositive failed: %v: %s", runErr, stderr.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.UseNumber()
	var output map[string]any
	if err := decoder.Decode(&output); err != nil {
		return result, err
	}
	index, err := historicalLiveUint(output["directive_index"])
	if err != nil || int(index) != control.Index || output["source_id"] != control.SourceID || output["backend"] != dialect || output["passed"] != true {
		return result, fmt.Errorf("historical typepositive output proof missing")
	}
	result["program_output"] = output
	result["passed"] = true
	return result, nil
}
