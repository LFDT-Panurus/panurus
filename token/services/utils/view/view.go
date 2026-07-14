/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file provides utilities for running views with timeout support.
// RunViewWithTimeout wraps view execution with context-based timeout handling.
package view

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type viewContext = view.Context

type contextWrapper struct {
	viewContext
	ctx context.Context
}

func (c *contextWrapper) Context() context.Context {
	return c.ctx
}

func RunViewWithTimeout(ctx view.Context, v view.View, timeout time.Duration, opts ...view.RunViewOption) (any, error) {
	if timeout == 0 {
		return ctx.RunView(v, opts...)
	}

	timeoutContext, cancel := context.WithTimeout(ctx.Context(), timeout)
	defer cancel()
	newContext := &contextWrapper{
		viewContext: ctx,
		ctx:         timeoutContext,
	}

	return newContext.RunView(v, opts...)
}

// RunView runs passed view within the passed Context and using the passed options in a separate goroutine
func RunView(logger logging.Logger, context view.Context, view view.View, opts ...view.RunViewOption) {
	defer func() {
		if r := recover(); r != nil {
			logger.DebugfContext(context.Context(), "panic in RunView: %v", r)
		}
	}()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.DebugfContext(context.Context(), "panic in RunView: %v", r)
			}
		}()
		_, err := context.RunView(view, opts...)
		if err != nil {
			logger.ErrorfContext(context.Context(), "failed to run view: %s", err)
		}
	}()
}
