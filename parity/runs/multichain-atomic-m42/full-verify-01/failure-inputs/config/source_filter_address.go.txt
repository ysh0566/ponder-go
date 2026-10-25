package config

import (
	"fmt"

	"ponder.local/ponder-go/filter"
)

// SourceAddress is the closed address union. Factory parents use its narrower
// plain-address union, while a log filter can also select a factory.
type SourceAddress interface{ sourceAddressValue() any }
type SourceParentAddress interface {
	SourceAddress
	sourceParentAddress()
}
type SourceWildcardAddress struct{}
type SourceLiteralAddress string
type SourceAddressList []string
type SourceFactoryAddress struct{ fields filter.Object }

func (SourceWildcardAddress) sourceAddressValue() any  { return filter.Undefined }
func (a SourceLiteralAddress) sourceAddressValue() any { return string(a) }
func (a SourceAddressList) sourceAddressValue() any {
	result := []any{}
	for _, address := range a {
		result = append(result, address)
	}
	return result
}
func (a SourceFactoryAddress) sourceAddressValue() any { return a.fields }
func (SourceWildcardAddress) sourceParentAddress()     {}
func (SourceLiteralAddress) sourceParentAddress()      {}
func (SourceAddressList) sourceParentAddress()         {}

func sourceAddressFromValue(value any, allowFactory bool) (SourceAddress, error) {
	if filter.IsUndefined(value) {
		return SourceWildcardAddress{}, nil
	}
	switch v := value.(type) {
	case string:
		return SourceLiteralAddress(v), nil
	case []string:
		return SourceAddressList(append([]string{}, v...)), nil
	case []any:
		result := SourceAddressList{}
		for _, entry := range v {
			address, ok := entry.(string)
			if !ok {
				return nil, fmt.Errorf("source address array must contain strings")
			}
			result = append(result, address)
		}
		return result, nil
	case map[string]any:
		if allowFactory && filter.IsAddressFactory(v) {
			return SourceFactoryAddress{fields: v}, nil
		}
	}
	return nil, fmt.Errorf("invalid source address union value %T", value)
}

// Address and its setters belong to the log-filter variant. SourceFilter must
// first be narrowed, and a factory address must be narrowed again before its
// parent, location or ID can be accessed.
func (f SourceLogFilter) Address() (SourceAddress, error) {
	value, exists := f.fields["address"]
	if !exists {
		value = filter.Undefined
	}
	return sourceAddressFromValue(value, true)
}
func (f SourceLogFilter) SetAddress(address SourceAddress) error {
	if f.fields == nil || address == nil {
		return fmt.Errorf("initialized log filter and source address required")
	}
	f.fields["address"] = address.sourceAddressValue()
	return nil
}
func (f SourceFactoryAddress) ID() string { id, _ := f.fields["id"].(string); return id }
func (f SourceFactoryAddress) SetParentAddress(address SourceParentAddress) error {
	if f.fields == nil || address == nil {
		return fmt.Errorf("initialized factory and parent address required")
	}
	f.fields["address"] = address.sourceAddressValue()
	return nil
}
func (f SourceFactoryAddress) SetChildAddressLocation(location string) error {
	if f.fields == nil {
		return fmt.Errorf("initialized source factory required")
	}
	f.fields["childAddressLocation"] = location
	return nil
}

// CompileSourceFilter compiles the explicit source-shaped filter inputs used
// by public synchronization APIs. It does not claim config/ABI builder parity.
// Value snapshots stay isolated; variant setters update the actual callback's
// retained filter, which its consumer reads afterwards.
func CompileSourceFilter(value filter.Object) (SourceFilter, error) {
	fields := sourceCopyObject(value)
	base := sourceFilterBase{fields: fields}
	switch fields["type"] {
	case "log":
		return SourceLogFilter{sourceFilterBase: base}, nil
	case "trace":
		return SourceTraceFilter{sourceFilterBase: base}, nil
	case "transaction":
		return SourceTransactionFilter{sourceFilterBase: base}, nil
	case "transfer":
		return SourceTransferFilter{sourceFilterBase: base}, nil
	case "block":
		interval, ok := sourceNumber(fields["interval"])
		if !ok {
			return nil, fmt.Errorf("source block interval required")
		}
		offset, ok := sourceNumber(fields["offset"])
		if !ok {
			return nil, fmt.Errorf("source block offset required")
		}
		return SourceBlockFilter{sourceFilterBase: base, Interval: interval, Offset: offset}, nil
	default:
		return nil, fmt.Errorf("unknown source filter kind %v", fields["type"])
	}
}
