//go:build simulation

package runtime

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type simulationArchive struct {
	Blocks []Block
	Logs   []Log
}

// This archive projects the exact address/topic query and the block fields used
// by this application. Reject broader queries rather than returning partial data.
func (a simulationArchive) transport(chainID uint64, address string, selectors ...string) rpc.TransportFunc {
	byNumber := map[string]Block{}
	byHash := map[string][]Log{}
	for _, block := range a.Blocks {
		byNumber[block.Number] = block
		byHash[block.Hash] = []Log{}
	}
	for _, log := range a.Logs {
		byHash[log.BlockHash] = append(byHash[log.BlockHash], log)
	}
	byNumber["latest"] = a.Blocks[len(a.Blocks)-1]
	return func(_ context.Context, method string, params any) (json.RawMessage, error) {
		switch method {
		case "eth_chainId":
			return json.Marshal(Hex(chainID))
		case "eth_getBlockByNumber":
			args, ok := params.([]any)
			if ok && len(args) == 2 {
				block, found := byNumber[fmt.Sprint(args[0])]
				if found {
					return json.Marshal(block)
				}
			}
		case "eth_getLogs":
			raw, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			var filters []struct {
				BlockHash string
				Address   []string
				Topics    [][]string
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&filters); err != nil {
				return nil, err
			}
			if len(filters) == 1 {
				f := filters[0]
				logs, found := byHash[f.BlockHash]
				if !found || len(f.Address) != 1 || !strings.EqualFold(f.Address[0], address) ||
					len(f.Topics) != 1 || len(f.Topics[0]) == 0 {
					break
				}
				for _, selector := range f.Topics[0] {
					if !slices.Contains(selectors, selector) {
						return nil, fmt.Errorf("uncaptured topic %s for chain %d", selector, chainID)
					}
				}
				matches := make([]Log, 0, len(logs))
				for _, log := range logs {
					if slices.Contains(f.Topics[0], log.Topics[0]) {
						matches = append(matches, log)
					}
				}
				return json.Marshal(matches)
			}
		}
		return nil, fmt.Errorf("request outside captured range and filter: chain %d %s %v", chainID, method, params)
	}
}

func readSimulationGzip(t *testing.T, filename string, target any) {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	dec := json.NewDecoder(reader)
	dec.UseNumber()
	if err := dec.Decode(target); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Fatalf("%s: unexpected trailing data or gzip error: %v", filename, err)
	}
}

func simulationStore(t *testing.T, dialect string, model schema.Schema, ordering schema.Ordering) *store.Store {
	t.Helper()
	if dialect == "postgres" && os.Getenv("PONDER_TEST_POSTGRES") == "" {
		t.Fatal("set PONDER_TEST_POSTGRES to a disposable PostgreSQL database")
	}
	backend := stagedBackend(t, context.Background(), dialect, "memory://")
	t.Cleanup(func() { _ = backend.Close() })
	ns := fmt.Sprintf("ponder_go_sim_%x", time.Now().UnixNano())
	db, err := store.New(t.Context(), backend, model, ns, ordering)
	if err != nil {
		t.Fatal(err)
	}
	if dialect == "postgres" {
		t.Cleanup(func() {
			if err := backend.Exec(context.Background(), `DROP SCHEMA "`+ns+`" CASCADE`); err != nil {
				t.Error(err)
			}
		})
	}
	return db
}
