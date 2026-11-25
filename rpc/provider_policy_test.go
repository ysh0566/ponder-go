package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type providerPolicyCase struct {
	ID, Name, Provider, Env, URL string
	Request                      struct {
		Method string
		Params []map[string]any
	}
	Expect struct {
		ErrorClass, Message string
		Logs, RangeCount    *int
		ShouldRetry         *bool
		FirstRange          *LogRange
		Plan                *LogsRetryPlan
	}
}

func TestUpstreamLiveProviderPolicies(t *testing.T) {
	var fixture struct {
		RPCErrorCodes []int
		Cases         []providerPolicyCase
	}
	raw, err := os.ReadFile("../testdata/rpc/provider-policy-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var endpoints map[string]string
	file := os.Getenv("PONDER_TEST_PROVIDER_POLICY_URLS")
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, &endpoints); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range fixture.Cases {
		t.Run(c.ID, func(t *testing.T) {
			var endpoint string
			switch {
			case file != "":
				endpoint = endpoints[c.Provider]
			case os.Getenv("PONDER_TEST_LIVE_PROVIDER_POLICIES") != "1":
				t.Skip("enable live provider-policy tests or supply a private endpoint map")
			case c.Env != "":
				endpoint = os.Getenv(c.Env)
			default:
				endpoint = c.URL
			}
			if endpoint == "" {
				t.Skipf("no endpoint for %s (%s)", c.Provider, c.Env)
			}
			u, err := url.Parse(endpoint)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				t.Fatal("invalid provider HTTP endpoint")
			}
			client := New(endpoint)
			client.MaxHTTPResponseBytes = 128 << 20
			client.HTTP = &http.Client{Timeout: 20 * time.Second}
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			var logs []json.RawMessage
			callErr := client.Call(ctx, c.Request.Method, c.Request.Params, &logs)
			if want := c.Expect.Logs; want != nil {
				if callErr != nil {
					t.Fatalf("%s: expected %d logs, got %T", c.Name, *want, callErr)
				}
				if len(logs) != *want {
					t.Fatalf("%s: got %d logs, want %d", c.Name, len(logs), *want)
				}
				return
			}
			if callErr == nil {
				t.Fatalf("%s: expected provider rejection, got %d logs", c.Name, len(logs))
			}
			var rpcErr *Error
			var httpErr *HTTPStatusError
			var details []byte
			var class string
			switch {
			case errors.As(callErr, &httpErr):
				class = "HttpRequestError"
				details = httpErr.Details
				t.Logf("%s: HTTP %d", c.Name, httpErr.StatusCode)
			case errors.As(callErr, &rpcErr):
				class = "RpcRequestError"
				if slices.Contains(fixture.RPCErrorCodes, rpcErr.Code) {
					class = "RpcError"
				}
				details, err = json.Marshal(rpcErr)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("%s: JSON-RPC %d", c.Name, rpcErr.Code)
			default:
				t.Fatalf("%s: transport returned %T without provider details", c.Name, callErr)
			}
			// These viem subclasses carry specific standardized JSON-RPC codes.
			wantClass := c.Expect.ErrorClass
			switch wantClass {
			case "InvalidInputRpcError", "InvalidParamsRpcError", "LimitExceededRpcError":
				codes := map[string]int{"InvalidInputRpcError": -32000, "InvalidParamsRpcError": -32602, "LimitExceededRpcError": -32005}
				if rpcErr == nil || rpcErr.Code != codes[wantClass] {
					t.Errorf("expected %s", wantClass)
				}
			default:
				if wantClass != "" && class != wantClass {
					t.Errorf("error class %s, want %s", class, wantClass)
				}
			}
			if !strings.Contains(string(details), c.Expect.Message) {
				t.Errorf("provider details do not contain %q", c.Expect.Message)
			}
			plan, err := GetLogsRetryHelper(c.Request.Params[0], callErr)
			if err != nil {
				t.Fatal(err)
			}
			if want := c.Expect.Plan; want != nil && !reflect.DeepEqual(plan, *want) {
				t.Errorf("retry plan differs from original expectation: retry=%t suggested=%t ranges=%d", plan.ShouldRetry, plan.IsSuggestedRange, len(plan.Ranges))
			}
			if want := c.Expect.ShouldRetry; want != nil && plan.ShouldRetry != *want {
				t.Errorf("retry=%t, want %t", plan.ShouldRetry, *want)
			}
			if want := c.Expect.RangeCount; want != nil && len(plan.Ranges) != *want {
				t.Errorf("got %d ranges, want %d", len(plan.Ranges), *want)
			}
			if want := c.Expect.FirstRange; want != nil && (len(plan.Ranges) == 0 || plan.Ranges[0] != *want) {
				t.Errorf("expected first range %+v", *want)
			}
		})
	}
}
