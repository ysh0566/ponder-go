package schema

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// ValidateNamespaces checks user-selected indexing and views names before a
// project starts. Empty names let a native app choose its default namespace.
func ValidateNamespaces(index, views string) error {
	if views != "" && views == index {
		return fmt.Errorf("Views schema cannot be the same as the schema.")
	}
	for _, item := range []struct{ name, role string }{
		{index, "schema"}, {views, "views schema"},
	} {
		switch item.name {
		case "ponder_sync", "ponder_rpc_cache_1":
			return fmt.Errorf("Invalid %s name. %q is a reserved schema name.", item.role, item.name)
		}
	}
	for _, item := range []struct{ name, label string }{{index, "Schema"}, {views, "Views schema"}} {
		if len(utf16.Encode([]rune(item.name))) > 45 {
			return fmt.Errorf("%s name cannot be longer than 45 characters.", item.label)
		}
	}
	return nil
}

// ValidatePublishedNamespace checks the stable namespace used by query views.
func ValidatePublishedNamespace(namespace, views string) error {
	if err := ValidateNamespaces(namespace, views); err != nil {
		return err
	}
	if !Identifier(views) || views == "public" || strings.HasPrefix(views, "pg_") || views == "information_schema" {
		return fmt.Errorf("invalid published views schema %q", views)
	}
	return nil
}
