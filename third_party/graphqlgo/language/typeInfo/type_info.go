package typeInfo

import (
	"github.com/ysh0566/ponder-go/third_party/graphqlgo/language/ast"
)

// TypeInfoI defines the interface for TypeInfo Implementation
type TypeInfoI interface {
	Enter(node ast.Node)
	Leave(node ast.Node)
}
