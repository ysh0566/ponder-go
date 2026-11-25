package graphql

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"

	gql "github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/lexer"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"
)

//go:embed playground.html
var playground string

type Request struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName,omitempty"`
	Variables     map[string]any `json:"variables,omitempty"`
}

func problem(message string) *gql.Result {
	return &gql.Result{Errors: []gqlerrors.FormattedError{gqlerrors.NewFormattedError(message)}}
}

// Execute applies operation limits and validates the document before opening a database transaction.
func (s *Server) Execute(ctx context.Context, r Request) *gql.Result {
	src := source.NewSource(&source.Source{Body: []byte(r.Query), Name: "GraphQL request"})
	next := lexer.Lex(src)
	for count := 0; ; count++ {
		token, err := next(0)
		if err != nil {
			return problem(err.Error())
		}
		if token.Kind == lexer.EOF {
			break
		}
		if count >= s.options.MaxOperationTokens {
			return problem(fmt.Sprintf("Syntax Error: Token limit of %d exceeded.", s.options.MaxOperationTokens))
		}
	}
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		return problem(err.Error())
	}
	if err := s.checkLimits(doc); err != nil {
		return problem(err.Error())
	}
	valid := gql.ValidateDocument(&s.schema, doc, nil)
	if !valid.IsValid {
		for i, e := range valid.Errors {
			valid.Errors[i].Message = unknownArg.ReplaceAllString(e.Message, `Unknown argument "$1" on field "$3.$2".`)
		}
		return &gql.Result{Errors: valid.Errors}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return gql.Execute(gql.ExecuteParams{Schema: s.schema, AST: doc, Context: context.WithValue(ctx, loaderKey{}, &requestLoaders{groups: map[string]*relationBatch{}}), OperationName: r.OperationName, Args: r.Variables})
}

var unknownArg = regexp.MustCompile(`^Unknown argument "([^"]+)" on field "([^"]+)" of type "([^"]+)"\.`)

type cost struct{ depth, aliases int }

// Cache fragment costs relative to their root to handle reuse at different depths.
func (s *Server) checkLimits(doc *ast.Document) error {
	fragments := map[string]*ast.FragmentDefinition{}
	for _, def := range doc.Definitions {
		if f, ok := def.(*ast.FragmentDefinition); ok {
			fragments[f.Name.Value] = f
		}
	}
	memo := map[string]cost{}
	active := map[string]bool{}
	var walk func(*ast.SelectionSet) (cost, error)
	walk = func(set *ast.SelectionSet) (cost, error) {
		var total cost
		if set == nil {
			return total, nil
		}
		for _, node := range set.Selections {
			var child cost
			var err error
			switch n := node.(type) {
			case *ast.Field:
				child, err = walk(n.SelectionSet)
				if n.Alias != nil && n.Name.Value != "__typename" && child.aliases < math.MaxInt {
					child.aliases++
				}
			case *ast.InlineFragment:
				child, err = walk(n.SelectionSet)
			case *ast.FragmentSpread:
				name := n.Name.Value
				if active[name] {
					return cost{}, fmt.Errorf("Cannot spread fragment %q within itself.", name)
				}
				if previous, ok := memo[name]; ok {
					child = previous
				} else if f := fragments[name]; f != nil {
					active[name] = true
					child, err = walk(f.SelectionSet)
					delete(active, name)
					child.depth++
					memo[name] = child
				}
			}
			if err != nil {
				return cost{}, err
			}
			total.depth = max(total.depth, child.depth+1)
			total.aliases += min(math.MaxInt-total.aliases, child.aliases)
		}
		return total, nil
	}
	for _, def := range doc.Definitions {
		op, ok := def.(*ast.OperationDefinition)
		if !ok {
			continue
		}
		n, err := walk(op.SelectionSet)
		if err != nil {
			return err
		}
		if n.depth > s.options.MaxOperationDepth {
			return fmt.Errorf("Syntax Error: Query depth limit of %d exceeded, found %d.", s.options.MaxOperationDepth, n.depth)
		}
		if n.aliases > s.options.MaxOperationAliases {
			return fmt.Errorf("Syntax Error: Aliases limit of %d exceeded, found %d.", s.options.MaxOperationAliases, n.aliases)
		}
	}
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, playground)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var req Request
	var result *gql.Result
	switch {
	case r.Method != http.MethodPost:
		result = problem("GraphQL requests require POST")
	case strings.Split(r.Header.Get("Content-Type"), ";")[0] == "application/graphql":
		b, err := io.ReadAll(r.Body)
		if err != nil {
			result = problem(err.Error())
		} else {
			req.Query = string(b)
		}
	default:
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			result = problem(err.Error())
		} else if err := decoder.Decode(new(any)); err != io.EOF {
			result = problem("Expected one GraphQL request")
		}
	}
	if result == nil {
		result = s.Execute(r.Context(), req)
	}
	json.NewEncoder(w).Encode(result)
}
