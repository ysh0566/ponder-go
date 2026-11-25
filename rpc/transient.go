package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

// A transportFailure marks an error from the RPC connection, so callers can
// retry acquisition without treating database or handler I/O errors as RPC.
type transportFailure struct{ cause error }

func (e *transportFailure) Error() string { return e.cause.Error() }
func (e *transportFailure) Unwrap() error { return e.cause }

func IsTransient(err error) bool {
	var transport *transportFailure
	if errors.As(err, &transport) || errors.Is(err, ErrBlockNotFound) {
		return true
	}
	var status *HTTPStatusError
	if errors.As(err, &status) {
		return status.StatusCode == 408 || status.StatusCode == 425 || status.StatusCode == 429 || status.StatusCode >= 500 && status.StatusCode < 600
	}
	var protocol *Error
	return errors.As(err, &protocol) && (protocol.Code == 429 || protocol.Code == -32005 && strings.Contains(strings.ToLower(protocol.Message), "rate"))
}

func markTransport(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var request *url.Error
	cause := err
	if errors.As(err, &request) {
		cause = request.Err
	}
	var network net.Error
	var closed *websocket.CloseError
	if errors.As(cause, &closed) {
		switch closed.Code {
		case websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived,
			websocket.CloseAbnormalClosure, websocket.CloseInternalServerErr, websocket.CloseServiceRestart, websocket.CloseTryAgainLater:
			return &transportFailure{err}
		}
		return err
	}
	if errors.As(cause, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &transportFailure{err}
	}
	return err
}
