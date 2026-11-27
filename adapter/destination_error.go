package adapter

import "errors"

// DestinationError is a dial error of an outbound whose proxy server answered,
// but could not reach the destination, for example an HTTP CONNECT proxy that
// replied 503. The path to the server works, so the error says nothing about
// the outbound's health, and outbound groups do not fail over on it.
type DestinationError struct {
	Err error
}

func (e *DestinationError) Error() string {
	return e.Err.Error()
}

func (e *DestinationError) Unwrap() error {
	return e.Err
}

// NewDestinationError marks err as a destination error.
func NewDestinationError(err error) error {
	if err == nil {
		return nil
	}
	return &DestinationError{Err: err}
}

// IsDestinationError reports whether err, or an error it wraps, is a
// DestinationError.
func IsDestinationError(err error) bool {
	var destinationError *DestinationError
	return errors.As(err, &destinationError)
}

// AsServerError turns a destination error into a plain error with the same
// message. A detour dials the server of the outbound that uses it, so a
// destination the detour cannot reach is that outbound's unreachable server.
func AsServerError(err error) error {
	if !IsDestinationError(err) {
		return err
	}
	return errors.New(err.Error())
}
