package filter

// HexTopic represents the source LogFilter's required single topic0 Hex.
// Undefined, nil and arrays are not assignable. Go's zero-value/omitted fields
// and arbitrary string conversions remain broader than the source Hex type.
type HexTopic string

// LogFilter is a native typed description for precompiled log bloom filters.
// The legacy Object API remains available for source deliberately-invalid
// runtime cases guarded by ts-expect-error in the original tests.
type LogFilter struct {
	ChainID               uint64
	SourceID              string
	Address               any
	Topic0                HexTopic
	Topic1                any
	Topic2                any
	Topic3                any
	FromBlock             *uint64
	ToBlock               *uint64
	HasTransactionReceipt bool
	Include               []string
}

func (f LogFilter) Object() Object {
	address := f.Address
	if address == nil {
		address = Undefined
	}
	var from, to any = Undefined, Undefined
	if f.FromBlock != nil {
		from = *f.FromBlock
	}
	if f.ToBlock != nil {
		to = *f.ToBlock
	}
	return Object{"type": "log", "chainId": f.ChainID, "sourceId": f.SourceID, "address": address, "topic0": string(f.Topic0), "topic1": f.Topic1, "topic2": f.Topic2, "topic3": f.Topic3, "fromBlock": from, "toBlock": to, "hasTransactionReceipt": f.HasTransactionReceipt, "include": append([]string{}, f.Include...)}
}
