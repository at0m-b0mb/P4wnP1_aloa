//go:build linux
// +build linux

package service

// recovery_interceptor.go -- turn a panic in an RPC handler into an error
// instead of a dead appliance.
//
// grpc-go deliberately does not recover panics raised inside a handler: the
// goroutine unwinds, nothing catches it, and the process exits. For a library
// that is a defensible default. For an appliance whose entire purpose is to be
// reachable while plugged into something, it means one bad request ends the
// engagement -- and this service shipped at least one handler whose body was
// literally panic("implement me").
//
// LIMIT, stated plainly: this catches panics. It does NOT catch a runtime
// out-of-memory fatal throw or the OOM killer, neither of which is a panic and
// neither of which recover() can intercept. Bounds-checking the inputs that
// drive allocations is the real mitigation for those; this is a backstop for
// genuine programming errors.

import (
	"context"
	"log"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// logRecoveredPanic records what happened with a stack, then converts it to an
// error. The stack goes to the journal because by the time the client sees
// codes.Internal the context is gone, and a bare "Internal" in a bug report is
// almost useless.
func logRecoveredPanic(what string, r interface{}) error {
	log.Printf("PANIC recovered in %s: %v\n%s", what, r, debug.Stack())
	return status.Errorf(codes.Internal,
		"internal error handling %s; the service survived and the details are in the journal", what)
}

func recoveryUnaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (resp interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			resp, err = nil, logRecoveredPanic(info.FullMethod, r)
		}
	}()
	return handler(ctx, req)
}

func recoveryStreamInterceptor(
	srv interface{},
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = logRecoveredPanic(info.FullMethod, r)
		}
	}()
	return handler(srv, ss)
}
