package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"sync"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
)

// SourceConfiguration is the explicit dynamic source-shaped configuration API.
// It does not claim the contextual TypeScript literal/type constraints. The
// native typed API and compiler obligations are separate from this adapter.
type SourceConfiguration struct{ value *sourceObject }

// SourceBuildOptions controls native clients created by the source builder.
type SourceBuildOptions struct{ RPCMaxConcurrency int }

// SourceConfigBuild retains actual native clients and canonical chain metadata.
type SourceConfigBuild struct{ value *sourceConfigBuild }

type SourceIndexingFunction struct {
	Name string `json:"name"`
	Fn   any    `json:"-"`
}

// ParseSourceConfiguration preserves object order and absent/null values.
func ParseSourceConfiguration(data []byte) (*SourceConfiguration, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := sourceReadJSON(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing source configuration data")
	}
	object, ok := value.(*sourceObject)
	if !ok {
		return nil, fmt.Errorf("source configuration must be an object")
	}
	return &SourceConfiguration{value: object}, nil
}

func sourceReadJSON(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := sourceNewObject()
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			value, err := sourceReadJSON(decoder)
			if err != nil {
				return nil, err
			}
			object.set(key.(string), value)
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		values := []any{}
		for decoder.More() {
			value, err := sourceReadJSON(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err := decoder.Token()
		return values, err
	default:
		return token, nil
	}
}

//go:embed chain_catalog.json
var sourceCatalogData []byte

var sourceCatalogOnce sync.Once
var sourceCatalog map[string]*sourceObject
var sourceCatalogError error

func loadSourceCatalog() (map[string]*sourceObject, error) {
	sourceCatalogOnce.Do(func() {
		var catalog struct {
			Baseline string
			Chains   []struct {
				ID         json.Number
				Descriptor json.RawMessage
			}
		}
		if err := json.Unmarshal(sourceCatalogData, &catalog); err != nil {
			sourceCatalogError = err
			return
		}
		if catalog.Baseline != "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334" {
			sourceCatalogError = fmt.Errorf("unexpected source chain catalog revision")
			return
		}
		sourceCatalog = map[string]*sourceObject{}
		for _, entry := range catalog.Chains {
			value, err := sourceDecodeWire(entry.Descriptor)
			if err != nil {
				sourceCatalogError = err
				return
			}
			object, ok := value.(*sourceObject)
			if !ok {
				sourceCatalogError = fmt.Errorf("invalid source chain descriptor")
				return
			}
			sourceCatalog[entry.ID.String()] = object
		}
	})
	return sourceCatalog, sourceCatalogError
}

func BuildSourceConfig(config *SourceConfiguration, options SourceBuildOptions) (*SourceConfigBuild, error) {
	if config == nil || config.value == nil {
		return nil, fmt.Errorf("source configuration is required")
	}
	catalog, err := loadSourceCatalog()
	if err != nil {
		return nil, err
	}
	builder := sourceBuilder{Catalog: catalog}
	result, err := builder.buildConfig(config.value, options.RPCMaxConcurrency)
	if err != nil {
		return nil, err
	}
	return &SourceConfigBuild{value: result}, nil
}

func (build *SourceConfigBuild) RPCClients() []*rpc.Client {
	return append([]*rpc.Client{}, build.value.RPCs...)
}

func (build *SourceConfigBuild) Value() map[string]any {
	return map[string]any{"chains": sourcePlain(build.value.Chains), "rpcs": sourcePlain(build.value.RPCs), "logs": sourcePlain(build.value.Logs)}
}

func (config *SourceConfiguration) Value() map[string]any {
	return sourcePlain(config.value).(map[string]any)
}

// Function/RPC values are intentionally opaque in JSON projections. Actual
// clients are retained separately and used for all native RPC operations.
func sourcePlain(value any) any {
	switch v := value.(type) {
	case *sourceObject:
		out := map[string]any{}
		for _, key := range v.keys {
			out[key] = sourcePlain(v.values[key])
		}
		return out
	case []*sourceObject:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sourcePlain(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sourcePlain(item)
		}
		return out
	case sourceFunctionRef:
		return map[string]any{"$function": true}
	case []*rpc.Client:
		out := make([]any, len(v))
		for i, client := range v {
			hostnames := []string{}
			for _, endpoint := range client.URLs {
				parsed, _ := url.Parse(endpoint)
				hostnames = append(hostnames, parsed.Hostname())
			}
			// This is an explicit opaque client projection, not a declaration
			// that the full source request/subscription API has been ported.
			out[i] = map[string]any{"$rpc": map[string]any{"hostnames": hostnames}}
		}
		return out
	default:
		if value != nil && reflect.ValueOf(value).Kind() == reflect.Func {
			return map[string]any{"$function": true}
		}
		if filter.IsUndefined(value) {
			return filter.Undefined
		}
		return value
	}
}
