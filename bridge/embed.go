// Package bridge exposes the PGlite adapter to native Go applications.
package bridge

import _ "embed"

// PGlite is run by Node when an application opens an embedded database.
//
//go:embed pglite.mjs
var PGlite string
