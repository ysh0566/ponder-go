package abi

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// BytesConsumed describes the head size of a parameter in the ABI encoding.
// Dynamic data contributes a 32-byte pointer; static tuples and arrays expand.
func BytesConsumed(p Parameter) (uint64, error) {
	if strings.HasSuffix(p.Type, "[]") {
		return 32, nil
	}
	if m := fixedArray.FindStringSubmatch(p.Type); m != nil {
		n, e := strconv.ParseUint(m[2], 10, 64)
		if e != nil {
			return 0, e
		}
		if n == 0 || !deeplyStatic(p) {
			return 32, nil
		}
		p.Type = m[1]
		size, e := BytesConsumed(p)
		if e != nil {
			return 0, e
		}
		if size != 0 && n > math.MaxUint64/size {
			return 0, fmt.Errorf("ABI head size overflows uint64")
		}
		return n * size, nil
	}
	if p.Type == "tuple" {
		if !deeplyStatic(p) {
			return 32, nil
		}
		var size uint64
		for _, c := range p.Components {
			n, e := BytesConsumed(c)
			if e != nil {
				return 0, e
			}
			if n > math.MaxUint64-size {
				return 0, fmt.Errorf("ABI head size overflows uint64")
			}
			size += n
		}
		return size, nil
	}
	if p.Type == "string" || p.Type == "address" || p.Type == "bool" || strings.HasPrefix(p.Type, "bytes") || strings.HasPrefix(p.Type, "uint") || strings.HasPrefix(p.Type, "int") {
		return 32, nil
	}
	return 0, fmt.Errorf("Type %q is not a valid decoding type.\nPlease provide a valid ABI type.", p.Type)
}

// NestedOffset follows the original head-offset algorithm for named tuple paths.
func NestedOffset(p Parameter, names []string) (uint64, error) {
	var consumed uint64
	for _, c := range p.Components {
		if len(names) > 0 && c.Name == names[0] {
			if len(names) == 1 {
				return consumed, nil
			}
			if c.Type != "tuple" {
				return 0, fmt.Errorf("Factory event parameter '%s.%s' is not valid for %s struct signature.", p.Name, strings.Join(names, "."), p.Name)
			}
			n, e := NestedOffset(c, names[1:])
			if e != nil {
				return 0, e
			}
			if n > math.MaxUint64-consumed {
				return 0, fmt.Errorf("ABI offset overflows uint64")
			}
			return consumed + n, nil
		}
		n, e := BytesConsumed(c)
		if e != nil {
			return 0, e
		}
		if n > math.MaxUint64-consumed {
			return 0, fmt.Errorf("ABI offset overflows uint64")
		}
		consumed += n
	}
	return 0, fmt.Errorf("Factory event parameter '%s.%s' not found in %s struct signature.", p.Name, strings.Join(names, "."), p.Name)
}
