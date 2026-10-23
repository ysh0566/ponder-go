package abi

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Parameter retains name presence: an absent tuple name and an explicitly empty
// name have different object/array behavior in the pinned source.
type Parameter struct {
	Name       string      `json:"name,omitempty"`
	Type       string      `json:"type"`
	Indexed    bool        `json:"indexed,omitempty"`
	Components []Parameter `json:"components,omitempty"`
	HasName    bool        `json:"-"`
}

func (p Parameter) MarshalJSON() ([]byte, error) {
	type plain Parameter
	if p.HasName || p.Name != "" {
		return json.Marshal(struct {
			Name string `json:"name"`
			plain
		}{Name: p.Name, plain: plain(p)})
	}
	return json.Marshal(plain(p))
}

func (p *Parameter) UnmarshalJSON(data []byte) error {
	type plain Parameter
	var value plain
	if e := json.Unmarshal(data, &value); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(data, &fields); e != nil {
		return e
	}
	*p = Parameter(value)
	_, p.HasName = fields["name"]
	return nil
}

type DecodeOptions struct {
	FormatAddress func(string) string
	// Out appends decoded parameters to an existing output slice, preserving
	// the source's reusable out buffer (including successful prefix on error).
	Out *[]any
}
type decoder struct {
	data          string
	index, offset int64
	reads, limit  int
	formatAddress func(string) string
	values        int
}

var fixedArray = regexp.MustCompile(`^(.*)\[(\d+)\]$`)
var dynamicArray = regexp.MustCompile(`^(.*)\[\]$`)

func deeplyStatic(p Parameter) bool {
	if p.Type == "string" || p.Type == "bytes" || strings.HasSuffix(p.Type, "[]") {
		return false
	}
	if p.Type == "tuple" {
		for _, c := range p.Components {
			if !deeplyStatic(c) {
				return false
			}
		}
	}
	if m := fixedArray.FindStringSubmatch(p.Type); m != nil {
		p.Type = m[1]
		return deeplyStatic(p)
	}
	return true
}
func (d *decoder) word() (string, error) {
	if d.reads > d.limit {
		return "", fmt.Errorf("Recursive read limit exceeded.")
	}
	d.reads++
	if d.index < 2 || d.index > int64(len(d.data))-64 {
		return "", fmt.Errorf("Invalid data length.")
	}
	return "0x" + d.data[d.index:d.index+64], nil
}
func quantity(word string) (int64, error) {
	n, ok := new(big.Int).SetString(strings.TrimPrefix(word, "0x"), 16)
	if !ok {
		return 0, fmt.Errorf("invalid ABI hex value")
	}
	if !n.IsInt64() || n.Int64() > 9007199254740991 {
		return 0, fmt.Errorf("ABI quantity is outside the safe integer range")
	}
	return n.Int64(), nil
}
func integer(word string, signed bool, size int) (any, error) {
	n, ok := new(big.Int).SetString(strings.TrimPrefix(word, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("invalid ABI hex value")
	}
	if signed && n.Bit(255) == 1 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	if size > 48 {
		return n, nil
	}
	if !n.IsInt64() || n.Int64() > 9007199254740991 || n.Int64() < -9007199254740991 {
		return nil, fmt.Errorf("ABI integer is outside the safe integer range")
	}
	return n.Int64(), nil
}
func DecodeParameters(params []Parameter, data string, options ...DecodeOptions) ([]any, error) {
	if len(data) <= 2 && len(params) > 0 {
		return nil, fmt.Errorf("Cannot decode zero data (\"0x\") with ABI parameters.")
	}
	if len(data) > 2 && len(data) < 66 {
		return nil, fmt.Errorf("Data size of %g bytes is too small for given parameters.", float64(len(data)-2)/2)
	}
	format := func(s string) string { return common.HexToAddress(s).Hex() }
	if len(options) > 0 && options[0].FormatAddress != nil {
		format = options[0].FormatAddress
	}
	d := decoder{data: data, index: 2, offset: 2, limit: len(data)/64 + 8192, formatAddress: format}
	out := []any{}
	if len(options) > 0 && options[0].Out != nil {
		out = *options[0].Out
	}
	for _, p := range params {
		if int64(len(data))-d.index < 64 {
			return nil, fmt.Errorf("Invalid data length.")
		}
		v, e := d.decode(p)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
		if len(options) > 0 && options[0].Out != nil {
			*options[0].Out = out
		}
	}
	return out, nil
}
func (d *decoder) decode(p Parameter) (any, error) {
	d.values++
	if d.values > (d.limit+1)*64 {
		return nil, fmt.Errorf("Recursive read limit exceeded.")
	}
	if m := fixedArray.FindStringSubmatch(p.Type); m != nil {
		length, e := strconv.ParseUint(m[2], 10, 64)
		if e != nil || length == 0 {
			return nil, fmt.Errorf("Invalid data length.")
		}
		p.Type = m[1]
		out := []any{}
		if !deeplyStatic(p) {
			oldOffset, oldIndex := d.offset, d.index
			word, e := d.word()
			if e != nil {
				return nil, e
			}
			offset, e := quantity(word)
			if e != nil {
				return nil, e
			}
			d.index = d.offset + offset*2
			d.offset += offset * 2
			for i := uint64(0); i < length; i++ {
				d.index = d.offset + int64(i)*64
				v, e := d.decode(p)
				if e != nil {
					return nil, e
				}
				out = append(out, v)
			}
			d.offset, d.index = oldOffset, oldIndex+64
			return out, nil
		}
		for i := uint64(0); i < length; i++ {
			if int64(len(d.data))-d.index < 64 {
				return nil, fmt.Errorf("Invalid data length.")
			}
			v, e := d.decode(p)
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		return out, nil
	}
	if m := dynamicArray.FindStringSubmatch(p.Type); m != nil {
		oldOffset, oldIndex := d.offset, d.index
		word, e := d.word()
		if e != nil {
			return nil, e
		}
		offset, e := quantity(word)
		if e != nil {
			return nil, e
		}
		d.index = d.offset + offset*2
		d.offset += offset*2 + 64
		word, e = d.word()
		if e != nil {
			return nil, e
		}
		length, e := quantity(word)
		if e != nil {
			return nil, e
		}
		d.index += 64
		p.Type = m[1]
		static := deeplyStatic(p)
		out := []any{}
		for i := int64(0); i < length; i++ {
			if !static {
				d.index = d.offset + i*64
			}
			if int64(len(d.data))-d.index < 64 {
				return nil, fmt.Errorf("Invalid data length.")
			}
			v, e := d.decode(p)
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		d.offset, d.index = oldOffset, oldIndex+64
		return out, nil
	}
	if p.Type == "tuple" {
		unnamed := len(p.Components) == 0
		for _, c := range p.Components {
			unnamed = unnamed || !c.HasName && c.Name == ""
		}
		array := []any{}
		object := map[string]any{}
		oldOffset, oldIndex := d.offset, d.index
		static := deeplyStatic(p)
		if !static {
			word, e := d.word()
			if e != nil {
				return nil, e
			}
			offset, e := quantity(word)
			if e != nil {
				return nil, e
			}
			d.offset += offset * 2
			d.index = d.offset
		}
		for _, c := range p.Components {
			v, e := d.decode(c)
			if e != nil {
				return nil, e
			}
			if unnamed {
				array = append(array, v)
			} else {
				object[c.Name] = v
			}
		}
		if !static {
			d.offset, d.index = oldOffset, oldIndex+64
		}
		if unnamed {
			return array, nil
		}
		return object, nil
	}
	if p.Type == "address" {
		if d.index < 2 || int64(len(d.data))-d.index < 64 {
			return nil, fmt.Errorf("Invalid data length.")
		}
		address := "0x" + d.data[d.index+24:d.index+64]
		d.index += 64
		return d.formatAddress(address), nil
	}
	if strings.HasPrefix(p.Type, "uint") || strings.HasPrefix(p.Type, "int") {
		signed := strings.HasPrefix(p.Type, "int")
		sizeText := strings.Split(p.Type, "int")[1]
		size := 256
		if sizeText != "" {
			size, _ = strconv.Atoi(sizeText)
		}
		word, e := d.word()
		if e != nil {
			return nil, e
		}
		d.index += 64
		return integer(word, signed, size)
	}
	if strings.HasPrefix(p.Type, "bytes") && len(p.Type) > 5 {
		size, e := strconv.ParseInt(strings.TrimPrefix(p.Type, "bytes"), 10, 64)
		if e != nil || size < 0 || d.index < 2 || int64(len(d.data))-d.index < size*2 {
			return nil, fmt.Errorf("Invalid data length.")
		}
		value := "0x" + d.data[d.index:d.index+size*2]
		d.index += 64
		return value, nil
	}
	if p.Type == "bool" {
		word, e := d.word()
		if e != nil {
			return nil, e
		}
		d.index += 64
		zero := "0x" + strings.Repeat("0", 64)
		one := "0x" + strings.Repeat("0", 63) + "1"
		if word != zero && word != one {
			return nil, fmt.Errorf("Hex value %q is not a valid boolean. The hex value must be \"0x0\" (false) or \"0x1\" (true).", word)
		}
		return word == one, nil
	}
	if p.Type == "string" || p.Type == "bytes" {
		oldIndex := d.index
		word, e := d.word()
		if e != nil {
			return nil, e
		}
		offset, e := quantity(word)
		if e != nil {
			return nil, e
		}
		d.index = d.offset + offset*2
		word, e = d.word()
		if e != nil {
			return nil, e
		}
		length, e := quantity(word)
		if e != nil {
			return nil, e
		}
		d.index += 64
		if d.index < 2 || length*2 > int64(len(d.data))-d.index {
			return nil, fmt.Errorf("Invalid data length.")
		}
		value := d.data[d.index : d.index+length*2]
		d.index = oldIndex + 64
		if p.Type == "bytes" {
			return "0x" + value, nil
		}
		raw, e := hex.DecodeString(value)
		if e != nil {
			return nil, e
		}
		return string(raw), nil
	}
	return nil, fmt.Errorf("Type %q is not a valid decoding type.\nPlease provide a valid ABI type.", p.Type)
}
func DecodeParameter(p Parameter, data string, options ...DecodeOptions) (any, error) {
	if len(data) <= 2 {
		return nil, fmt.Errorf("Cannot decode zero data (\"0x\") with ABI parameters.")
	}
	if len(data) != 66 {
		return nil, fmt.Errorf("Invalid data length. Expected 66 bytes, got %d", len(data))
	}
	if p.Type == "bool" {
		return data == "0x"+strings.Repeat("0", 63)+"1", nil
	}
	if p.Type != "address" && !strings.HasPrefix(p.Type, "uint") && !strings.HasPrefix(p.Type, "int") && !(strings.HasPrefix(p.Type, "bytes") && len(p.Type) > 5) {
		return nil, fmt.Errorf("Type %q is not a valid decoding type.\nPlease provide a valid ABI type.", p.Type)
	}
	out, e := DecodeParameters([]Parameter{p}, data, options...)
	if e != nil {
		return nil, e
	}
	return out[0], nil
}

type Event struct {
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	Inputs    []Parameter `json:"inputs"`
	Anonymous bool        `json:"anonymous,omitempty"`
}

func formatParameter(p Parameter) string {
	typ := p.Type
	if strings.HasPrefix(typ, "tuple") {
		parts := []string{}
		for _, c := range p.Components {
			parts = append(parts, formatParameter(c))
		}
		typ = "(" + strings.Join(parts, ", ") + ")" + strings.TrimPrefix(typ, "tuple")
	}
	if p.Name != "" {
		typ += " " + p.Name
	}
	return typ
}
func formatEvent(e Event) string {
	parts := []string{}
	for _, p := range e.Inputs {
		parts = append(parts, formatParameter(p))
	}
	return e.Name + "(" + strings.Join(parts, ", ") + ")"
}
func DecodeEventLog(event Event, topics []string, data string) (any, error) {
	args := make([]any, len(event.Inputs))
	unnamed := false
	non := []Parameter{}
	nonIndices := []int{}
	topicIndex := 1
	for i, p := range event.Inputs {
		unnamed = unnamed || p.Name == ""
		if !p.Indexed {
			non = append(non, p)
			nonIndices = append(nonIndices, i)
			continue
		}
		if topicIndex >= len(topics) || topics[topicIndex] == "" {
			parameter := ""
			if p.Name != "" {
				parameter = " " + strconv.Quote(p.Name)
			}
			return nil, fmt.Errorf("Expected a topic for indexed event parameter%s on event %q.", parameter, formatEvent(event))
		}
		topic := topics[topicIndex]
		topicIndex++
		if p.Type == "string" || p.Type == "bytes" || p.Type == "tuple" || fixedArray.MatchString(p.Type) || dynamicArray.MatchString(p.Type) {
			args[i] = topic
		} else {
			v, e := DecodeParameter(p, topic, DecodeOptions{FormatAddress: strings.ToLower})
			if e != nil {
				return nil, e
			}
			args[i] = v
		}
	}
	if len(non) > 0 {
		if data == "" || data == "0x" {
			return nil, fmt.Errorf("Data size of 0 bytes is too small for non-indexed event parameters.")
		}
		out, e := DecodeParameters(non, data, DecodeOptions{FormatAddress: strings.ToLower})
		if e != nil {
			return nil, e
		}
		for i, value := range out {
			args[nonIndices[i]] = value
		}
	}
	if len(args) == 0 {
		return nil, nil
	}
	if unnamed {
		return args, nil
	}
	out := map[string]any{}
	for i, p := range event.Inputs {
		out[p.Name] = args[i]
	}
	return out, nil
}
