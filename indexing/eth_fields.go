package indexing

import (
	"github.com/ysh0566/ponder-go/abi"
	"math/big"
)

// EventBlock reads Ethereum block fields from the current callback.
type EventBlock struct{ fields eventFields }

func (v EventView) BlockFields() (EventBlock, error) {
	fields, err := eventComponent(v, "block")
	return EventBlock{fields}, err
}
func (v EventBlock) BaseFeePerGas() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "baseFeePerGas", true)
}
func (v EventBlock) Difficulty() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "difficulty", false)
}
func (v EventBlock) GasLimit() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "gasLimit", false)
}
func (v EventBlock) GasUsed() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "gasUsed", false)
}
func (v EventBlock) Number() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "number", false)
}
func (v EventBlock) Size() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "size", false)
}
func (v EventBlock) Timestamp() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "timestamp", false)
}
func (v EventBlock) TotalDifficulty() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "totalDifficulty", true)
}
func (v EventBlock) ExtraData() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "extraData", false)
}
func (v EventBlock) Hash() (abi.Hex, error) { return readEventField[abi.Hex](v.fields, "hash", false) }
func (v EventBlock) LogsBloom() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "logsBloom", false)
}
func (v EventBlock) Miner() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "miner", false)
}
func (v EventBlock) ParentHash() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "parentHash", false)
}
func (v EventBlock) ReceiptsRoot() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "receiptsRoot", false)
}
func (v EventBlock) StateRoot() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "stateRoot", false)
}
func (v EventBlock) TransactionsRoot() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "transactionsRoot", false)
}
func (v EventBlock) MixHash() (*abi.Hex, error) {
	return readEventField[*abi.Hex](v.fields, "mixHash", true)
}
func (v EventBlock) Nonce() (*abi.Hex, error) {
	return readEventField[*abi.Hex](v.fields, "nonce", true)
}
func (v EventBlock) Sha3Uncles() (*abi.Hex, error) {
	return readEventField[*abi.Hex](v.fields, "sha3Uncles", true)
}

// EventLog reads Ethereum log fields from the current callback.
type EventLog struct{ fields eventFields }

func (v EventView) LogFields() (EventLog, error) {
	fields, err := eventComponent(v, "log")
	return EventLog{fields}, err
}
func (v EventLog) Address() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "address", false)
}
func (v EventLog) Data() (abi.Hex, error) { return readEventField[abi.Hex](v.fields, "data", false) }
func (v EventLog) LogIndex() (uint64, error) {
	return readEventField[uint64](v.fields, "logIndex", false)
}
func (v EventLog) Removed() (bool, error) { return readEventField[bool](v.fields, "removed", false) }
func (v EventLog) Topics() ([]abi.Hex, error) {
	return readEventField[[]abi.Hex](v.fields, "topics", false)
}

// EventTransaction reads Ethereum transaction fields from the current callback.
type EventTransaction struct{ fields eventFields }

func (v EventView) TransactionFields() (EventTransaction, error) {
	fields, err := eventComponent(v, "transaction")
	return EventTransaction{fields}, err
}
func (v EventTransaction) From() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "from", false)
}
func (v EventTransaction) Hash() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "hash", false)
}
func (v EventTransaction) Input() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "input", false)
}
func (v EventTransaction) R() (*abi.Hex, error) { return readEventField[*abi.Hex](v.fields, "r", true) }
func (v EventTransaction) S() (*abi.Hex, error) { return readEventField[*abi.Hex](v.fields, "s", true) }
func (v EventTransaction) To() (*abi.Hex, error) {
	return readEventField[*abi.Hex](v.fields, "to", true)
}
func (v EventTransaction) Gas() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "gas", false)
}
func (v EventTransaction) V() (*big.Int, error) { return readEventField[*big.Int](v.fields, "v", true) }
func (v EventTransaction) Value() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "value", false)
}
func (v EventTransaction) GasPrice() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "gasPrice", true)
}
func (v EventTransaction) MaxFeePerGas() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "maxFeePerGas", true)
}
func (v EventTransaction) MaxPriorityFeePerGas() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "maxPriorityFeePerGas", true)
}
func (v EventTransaction) Nonce() (uint64, error) {
	return readEventField[uint64](v.fields, "nonce", false)
}
func (v EventTransaction) TransactionIndex() (uint64, error) {
	return readEventField[uint64](v.fields, "transactionIndex", false)
}
func (v EventTransaction) Type() (string, error) {
	return readEventField[string](v.fields, "type", false)
}
func (v EventTransaction) AccessList() ([]AccessListEntry, error) {
	return readEventField[[]AccessListEntry](v.fields, "accessList", true)
}

// EventReceipt reads Ethereum transactionReceipt fields from the current callback.
type EventReceipt struct{ fields eventFields }

func (v EventView) ReceiptFields() (EventReceipt, error) {
	fields, err := eventComponent(v, "transactionReceipt")
	return EventReceipt{fields}, err
}
func (v EventReceipt) ContractAddress() (*abi.Hex, error) {
	return readEventField[*abi.Hex](v.fields, "contractAddress", true)
}
func (v EventReceipt) To() (*abi.Hex, error) { return readEventField[*abi.Hex](v.fields, "to", true) }
func (v EventReceipt) CumulativeGasUsed() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "cumulativeGasUsed", false)
}
func (v EventReceipt) EffectiveGasPrice() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "effectiveGasPrice", false)
}
func (v EventReceipt) GasUsed() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "gasUsed", false)
}
func (v EventReceipt) From() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "from", false)
}
func (v EventReceipt) LogsBloom() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "logsBloom", false)
}
func (v EventReceipt) Status() (string, error) {
	return readEventField[string](v.fields, "status", false)
}
func (v EventReceipt) Type() (string, error) { return readEventField[string](v.fields, "type", false) }

// EventTrace reads Ethereum trace fields from the current callback.
type EventTrace struct{ fields eventFields }

func (v EventView) TraceFields() (EventTrace, error) {
	fields, err := eventComponent(v, "trace")
	return EventTrace{fields}, err
}
func (v EventTrace) Type() (string, error)  { return readEventField[string](v.fields, "type", false) }
func (v EventTrace) From() (abi.Hex, error) { return readEventField[abi.Hex](v.fields, "from", false) }
func (v EventTrace) Input() (abi.Hex, error) {
	return readEventField[abi.Hex](v.fields, "input", false)
}
func (v EventTrace) To() (*abi.Hex, error) { return readEventField[*abi.Hex](v.fields, "to", true) }
func (v EventTrace) Output() (*abi.Hex, error) {
	return readEventField[*abi.Hex](v.fields, "output", true)
}
func (v EventTrace) Gas() (*big.Int, error) { return readEventField[*big.Int](v.fields, "gas", false) }
func (v EventTrace) GasUsed() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "gasUsed", false)
}
func (v EventTrace) Value() (*big.Int, error) {
	return readEventField[*big.Int](v.fields, "value", true)
}
func (v EventTrace) TraceAddress() ([]uint64, error) {
	return readEventField[[]uint64](v.fields, "traceAddress", false)
}
