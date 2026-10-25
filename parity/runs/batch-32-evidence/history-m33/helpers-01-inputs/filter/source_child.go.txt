package filter

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
)

type SourceChildAddressError struct{ Message string }

func (e *SourceChildAddressError) Error() string { return e.Message }
func sourceChildMethod(value any, method, expression string) (string, error) {
	if IsUndefined(value) {
		return "", &SourceChildAddressError{fmt.Sprintf("Cannot read properties of undefined (reading '%s')", method)}
	}
	if value == nil {
		return "", &SourceChildAddressError{fmt.Sprintf("Cannot read properties of null (reading '%s')", method)}
	}
	text, ok := value.(string)
	if !ok {
		return "", &SourceChildAddressError{expression + "." + method + " is not a function"}
	}
	return text, nil
}
func sourceChildSubstring(text string, start, end float64) string {
	units := utf16.Encode([]rune(text))
	index := func(value float64) int {
		if math.IsNaN(value) || value <= 0 {
			return 0
		}
		if value >= float64(len(units)) {
			return len(units)
		}
		return int(math.Trunc(value))
	}
	from, to := index(start), index(end)
	if from > to {
		from, to = to, from
	}
	return string(utf16.Decode(units[from:to]))
}

// GetSourceChildAddress preserves source substring bounds and its missing-topic
// error boundary. It returns an error for the caller's explicit factory catch.
// Standalone lone-surrogate JS strings are outside this Go string draft domain.
func GetSourceChildAddress(log, factory Object) (string, error) {
	location, err := sourceChildMethod(get(factory, "childAddressLocation"), "startsWith", "factory.childAddressLocation")
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(location, "offset") {
		suffix := strings.TrimSpace(location[6:])
		offset := float64(0)
		if suffix != "" {
			offset = num(suffix)
		}
		text, err := sourceChildMethod(get(log, "data"), "substring", "log.data")
		if err != nil {
			return "", err
		}
		start := 26 + offset*2
		return "0x" + sourceChildSubstring(text, start, start+40), nil
	}
	index := 3
	if location == "topic1" {
		index = 1
	} else if location == "topic2" {
		index = 2
	}
	topics, ok := arr(get(log, "topics"))
	if !ok {
		return "", &SourceChildAddressError{fmt.Sprintf("Cannot read properties of %s (reading '%d')", str(get(log, "topics")), index)}
	}
	var topic any = Undefined
	if index < len(topics) {
		topic = topics[index]
	}
	text, err := sourceChildMethod(topic, "substring", "log.topics[topicIndex]")
	if err != nil {
		return "", err
	}
	return "0x" + sourceChildSubstring(text, 26, 66), nil
}
