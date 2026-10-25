package abi

import "strings"

// EventEntries preserves source ABI order, duplicates and selector collisions.
func (a ABI) EventEntries() []Event { return append([]Event{}, a.eventEntries...) }

// EventSafeName follows the source overload boundary: anonymous events do not
// contribute to the duplicate-name set. Indexed modifiers belong before names.
func (a ABI) EventSafeName(item Event) string {
	count := 0
	for _, event := range a.eventEntries {
		if !event.Anonymous && event.Name == item.Name {
			count++
		}
	}
	if count < 2 {
		return item.Name
	}
	parts := make([]string, len(item.Inputs))
	for i, parameter := range item.Inputs {
		unnamed := parameter
		unnamed.Name = ""
		part := formatParameter(unnamed)
		if parameter.Indexed {
			part += " indexed"
		}
		if parameter.Name != "" {
			part += " " + parameter.Name
		}
		parts[i] = part
	}
	return item.Name + "(" + strings.Join(parts, ", ") + ")"
}

// FindEventSafe resolves only a source-safe name within this actual ABI.
// Config validation requires that exact membership before building topics.
func (a ABI) FindEventSafe(name string) (Event, bool) {
	for _, item := range a.eventEntries {
		if a.EventSafeName(item) == name {
			return item, true
		}
	}
	return Event{}, false
}
