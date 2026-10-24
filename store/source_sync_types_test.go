package store

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
	"regexp"
	"time"
)

type sourceLiveTypeFile struct {
	File string `json:"file"`
	SHA  string `json:"sha256"`
}
type sourceLiveTypeBuild struct {
	Exit   int    `json:"exit_code"`
	Stderr string `json:"stderr"`
}
type sourceLiveTypeControl struct {
	SourceID           string `json:"source_id"`
	SourceSHA          string `json:"source_sha256"`
	Index              int    `json:"source_directive_index"`
	Positive, Negative sourceLiveTypeFile
	Binary             string              `json:"positive_binary"`
	BinarySHA          string              `json:"positive_binary_sha256"`
	BuildPositive      sourceLiveTypeBuild `json:"build_positive"`
	BuildNegative      sourceLiveTypeBuild `json:"build_negative"`
	Status             string              `json:"status"`
}

func sourceLiveHash(file string) (string, error) {
	raw, e := os.ReadFile(file)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func sourceLiveLoadTypes(root, sourceSHA string) ([]sourceLiveTypeControl, error) {
	var report struct {
		Directives    []sourceLiveTypeControl `json:"directives"`
		Inputs        []sourceLiveTypeFile    `json:"inputs"`
		SourceContext []sourceLiveTypeFile    `json:"source_context"`
	}
	// Decode source_context separately so both fields keep their own JSON key.
	var raw map[string]json.RawMessage
	data, e := os.ReadFile(filepath.Join(root, "parity/runs/sync-store-m32/type-compile.json"))
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(data, &raw); e != nil {
		return nil, e
	}
	if e = json.Unmarshal(raw["directives"], &report.Directives); e != nil {
		return nil, e
	}
	if e = json.Unmarshal(raw["inputs"], &report.Inputs); e != nil {
		return nil, e
	}
	if e = json.Unmarshal(raw["source_context"], &report.SourceContext); e != nil {
		return nil, e
	}
	if len(report.Directives) != 9 {
		return nil, fmt.Errorf("all9 exact native source type controls required")
	}
	for _, file := range append(report.Inputs, report.SourceContext...) {
		actual, e := sourceLiveHash(filepath.Join(root, file.File))
		if e != nil || actual != file.SHA {
			return nil, fmt.Errorf("native/source type context stale: %s", file.File)
		}
	}
	seen := map[string]bool{}
	for _, control := range report.Directives {
		key := fmt.Sprintf("%s/%d", control.SourceID, control.Index)
		if seen[key] || control.SourceSHA != sourceSHA || control.Status != "compiled_runtime_pending" || control.BuildPositive.Exit != 0 || control.BuildNegative.Exit <= 0 || !regexp.MustCompile(`cannot use \[\]string.*as rpc\.SourceCallParameters value`).MatchString(control.BuildNegative.Stderr) {
			return nil, fmt.Errorf("invalid actual parameter type proof %s", key)
		}
		seen[key] = true
		for _, file := range []sourceLiveTypeFile{control.Positive, control.Negative} {
			actual, e := sourceLiveHash(filepath.Join(root, file.File))
			if e != nil || actual != file.SHA {
				return nil, fmt.Errorf("native type fixture stale: %s", file.File)
			}
		}
		actual, e := sourceLiveHash(control.Binary)
		if e != nil || actual != control.BinarySHA {
			return nil, fmt.Errorf("compiled positive binary stale: %s", control.Binary)
		}
	}
	return report.Directives, nil
}
func sourceLiveRunTypes(ctx context.Context, root, dialect, endpoint, sourceID string, controls []sourceLiveTypeControl) ([]map[string]any, error) {
	results := []map[string]any{}
	for _, control := range controls {
		if control.SourceID != sourceID {
			continue
		}
		namespace := fmt.Sprintf("ponder_go_src_type_%x_%d", time.Now().UnixNano(), control.Index)
		input := map[string]any{"Backend": dialect, "DatabaseURL": os.Getenv("PONDER_TEST_POSTGRES"), "RPCURL": endpoint, "Namespace": namespace}
		raw, e := json.Marshal(input)
		if e != nil {
			return results, e
		}
		command := exec.CommandContext(ctx, control.Binary)
		command.Dir = root
		command.Env = os.Environ()
		command.Stdin = bytes.NewReader(raw)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		e = command.Run()
		output := stdout.Bytes()
		result := map[string]any{"source_id": sourceID, "directive_index": control.Index, "backend": dialect, "rpc_endpoint": endpoint, "namespace": namespace, "positive_binary_sha256": control.BinarySHA, "stdout": string(output), "stderr": stderr.String(), "exit_error": fmt.Sprint(e), "passed": false}
		results = append(results, result)
		if e != nil {
			return results, fmt.Errorf("actual typed request/storage/RPC control failed: %s/%d: %w: %s", sourceID, control.Index, e, output)
		}
		var observed struct {
			SourceID  string `json:"source_id"`
			Index     int    `json:"directive_index"`
			Passed    bool   `json:"passed"`
			RPCResult string `json:"rpc_result"`
		}
		if e = json.Unmarshal(output, &observed); e != nil || !observed.Passed || observed.SourceID != sourceID || observed.Index != control.Index || observed.RPCResult != "0x" {
			return results, fmt.Errorf("actual positive type execution report incomplete: %s", output)
		}
		result["passed"] = true
	}
	return results, nil
}
