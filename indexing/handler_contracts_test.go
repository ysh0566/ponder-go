package indexing

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/store"
)

type transferObservation struct {
	Type, Checkpoint, Callback, Address, Alice, Bob, Balance string
	ChainID                                                  uint64 `json:"chain_id"`
	Count                                                    uint64
	Before, After, Metadata                                  Object
	RawLog                                                   Object   `json:"raw_log"`
	FieldOrder                                               []string `json:"field_order"`
	CompleteBefore                                           bool     `json:"complete_before"`
	Has                                                      bool
}

func cachedTransfer(t *testing.T, backend string, c transferObservation) {
	t.Helper()
	ctx := t.Context()
	s := indexingReplayStore(t, backend)
	point, err := checkpoint.Decode(c.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	before := indexingObject(indexingUnmark(t, c.Before))
	rawLog := indexingObject(indexingUnmark(t, c.RawLog))
	block := indexingObject(before["block"])
	if !strings.EqualFold(rawLog["address"].(string), c.Address) {
		t.Fatal("deployed asset does not match mint log")
	}
	callback := events.Object{"type": "contract", "name": c.Callback, "filter": Object{"type": "log"}, "abiItem": c.Metadata}
	decoder := &events.Decoder{}
	decoded, err := decoder.DecodePayloads(Object{"id": c.ChainID}, []events.Object{callback}, []events.RawEvent{{ChainID: c.ChainID, Checkpoint: c.Checkpoint, Block: block, Log: rawLog, Transaction: indexingObject(before["transaction"])}})
	if err != nil || len(decoded) != 1 || decoder.Stats.LogSuccess != 1 {
		t.Fatalf("decode mint: %v, stats %+v", err, decoder.Stats)
	}
	args := decoded[0].(events.LogPayload).Args()
	indexingEqual(t, "native decoded args", args, before["args"])
	before["args"] = args
	body, err := events.PayloadOf(Object{"event": before, "type": c.Type})
	if err != nil {
		t.Fatal(err)
	}
	log, ok := body.(events.LogPayload)
	if !ok {
		t.Fatalf("unexpected payload %T", body)
	}
	profile := store.ProfileEvent{Callback: c.Callback, Count: c.Count, Type: c.Type, ChainID: c.ChainID, Data: store.Row(before), FieldOrder: map[string][]string{"args": c.FieldOrder}}
	err = s.Apply(ctx, store.Event{ID: body.ID(), Checkpoint: point}, func(w *store.Writer) error {
		db := w.Indexing(store.CacheOptions{Complete: true})
		if err := db.Cache.SetProfileEvent(profile); err != nil {
			return err
		}
		key := store.Row{"address": c.Bob}
		known, err := db.Cache.Has("account", key)
		if err != nil {
			return err
		}
		if known != c.CompleteBefore {
			return fmt.Errorf("new cache completeness differs")
		}
		n, ok := new(big.Int).SetString(c.Balance, 10)
		if !ok {
			return fmt.Errorf("invalid captured balance")
		}
		if _, err := db.InsertOne(ctx, "account", store.Row{"address": c.Alice, "balance": n}, store.Conflict{Action: store.ConflictNothing}); err != nil {
			return err
		}
		log.Args().(map[string]any)["to"] = c.Bob
		indexingEqual(t, "source event after mutation", before, indexingUnmark(t, c.After))
		if err := db.Cache.Flush(ctx); err != nil {
			return err
		}
		if err := db.Cache.Prefetch(ctx, []store.ProfileEvent{profile}); err != nil {
			return err
		}
		has, err := db.Cache.Has("account", key)
		if err != nil {
			return err
		}
		if has != c.Has || !has {
			return fmt.Errorf("unexpected prefetched key membership %t", has)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSourceHandlerDeclarations(t *testing.T) {
	read := func(file string, out any) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "testdata", "indexing", file))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
	var fixture struct {
		Cases []struct{ ID, Property, Name string }
	}
	read("handler-declarations.json", &fixture)
	var transfer struct {
		Cases map[string]transferObservation
	}
	read("cached-transfer.json", &transfer)
	service, _ := readHandlerServiceFixture(t)
	dir := t.TempDir()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	write := func(file, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module handler.test\n\ngo 1.25.0\n")
	write("go.work", fmt.Sprintf("go 1.25.0\nuse (\n.\n%q\n)\n", root))
	const prefix = `package main
import ("context";"errors";"github.com/ysh0566/ponder-go/events";"github.com/ysh0566/ponder-go/indexing")
var _ events.Payload
var _ indexing.Handler
var _ = errors.New
var _ = context.Background
func main(){
`
	check := func(t *testing.T, source string, diagnostics ...string) {
		t.Helper()
		write("main.go", prefix+source+"\n}\n")
		cmd := exec.Command("go", "build", "-o", "caller", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"))
		out, err := cmd.CombinedOutput()
		if len(diagnostics) == 0 {
			if err != nil {
				t.Fatalf("valid program rejected: %v\n%s", err, out)
			}
			if out, err := exec.Command(filepath.Join(dir, "caller")).CombinedOutput(); err != nil {
				t.Fatalf("valid caller failed: %v\n%s", err, out)
			}
			return
		}
		if err == nil {
			t.Fatal("invalid caller compiled")
		}
		for _, msg := range diagnostics {
			if !strings.Contains(string(out), msg) {
				t.Fatalf("expected %q in %s", msg, out)
			}
		}
	}
	for _, c := range fixture.Cases {
		t.Run(c.ID, func(t *testing.T) {
			switch c.Property {
			case "mockRejectedValue":
				selection := "EventCallbacks"
				if c.Name == "executeSetup() error" {
					selection = "SetupCallbacks"
				}
				start := `failure:=errors.New("handler error");calls:=0
				cb:=indexing.Object{"name":"original","fn":indexing.Handler(func(context.Context,*indexing.HandlerArgs)error{calls++;return failure})}
				build:=indexing.Build{` + selection + `:[][]indexing.Object{{cb}}}
				fn,err:=indexing.CallbackHandler(build.` + selection + `[0][0]);if err!=nil{panic(err)}`
				check(t, start+"\nif fn(context.Background(),&indexing.HandlerArgs{})!=failure || calls!=1{panic(\"handler identity changed\")}")
				check(t, start+"\nfn.MockRejectedValue(failure)", "fn.MockRejectedValue undefined", "indexing.Handler")
			case "transaction", "args":
				start := `payload,err:=events.PayloadOf(events.Object{"type":"log","event":events.Object{"transaction":events.Object{"maxPriorityFeePerGas":"1"},"args":events.Object{"to":"alice"}}})
				if err!=nil{panic(err)}`
				if c.Property == "transaction" {
					check(t, start+"\ndelete(payload.(events.LogPayload).Transaction(),\"maxPriorityFeePerGas\")")
					check(t, start+"\ndelete(payload.Transaction(),\"maxPriorityFeePerGas\")", "payload.Transaction undefined", "events.Payload")
				} else {
					check(t, start+"\npayload.(events.LogPayload).Args().(events.Object)[\"to\"]=\"bob\"")
					check(t, start+"\npayload.Args().(events.Object)[\"to\"]=\"bob\"", "payload.Args undefined", "events.Payload")
				}
			default:
				t.Fatal("unexpected source property", c.Property)
			}
			for _, backend := range []string{"postgres", "pglite"} {
				t.Run(backend, func(t *testing.T) {
					if c.Property == "args" {
						cachedTransfer(t, backend, transfer.Cases[backend])
						return
					}
					for _, record := range service.BackendCases[backend] {
						if record.Targeted && record.ID == c.ID {
							indexingReplayCase(t, backend, record)
							return
						}
					}
					t.Fatal("missing captured handler case", c.ID)
				})
			}
		})
	}
}
