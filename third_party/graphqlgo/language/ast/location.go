package ast

import (
	"github.com/ysh0566/ponder-go/third_party/graphqlgo/language/source"
)

type Location struct {
	Start  int
	End    int
	Source *source.Source
}

func NewLocation(loc *Location) *Location {
	if loc == nil {
		loc = &Location{}
	}
	return &Location{
		Start:  loc.Start,
		End:    loc.End,
		Source: loc.Source,
	}
}
