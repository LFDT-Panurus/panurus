/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package api

import (
	"fmt"
	"net/http"
)

// Error Enhanced Error interface giving extra-error information.
type Error interface {
	Error() string
	GetCode() int
	GetMessage() string
	GetLocation() string
	GetCause() error
}

// AppError Implements Error
type AppError struct {
	Code     int    `example:"400"           json:"code"`
	Message  string `example:"Error message" json:"message"`
	Cause    error  `json:"-"`
	Location string `json:"-"`
}

func (e *AppError) Error() string {
	if e.GetCause() == nil {
		return fmt.Sprintf("[CODE %d] %s", e.GetCode(), e.GetMessage())
	}

	return fmt.Sprintf("[CODE %d] %s: %s", e.GetCode(), e.GetMessage(), e.GetCause().Error())
}

// GetCode Returns the error code.
func (e *AppError) GetCode() int {
	return e.Code
}

// GetMessage Returns the error message.
func (e *AppError) GetMessage() string {
	return e.Message
}

// GetLocation Returns the error location.
func (e *AppError) GetLocation() string {
	return e.Location
}

// GetCause Returns the error.
func (e *AppError) GetCause() error {
	return e.Cause
}

func (e *AppError) String() string {
	return e.Error()
}

// NewBadRequestError Create a 400 Bad Request Error.
//
//	err error The error to wrap.
//	message string The generic message to show.
func NewBadRequestError(err error, message string) *AppError {
	return &AppError{
		Code:    http.StatusBadRequest,
		Message: message,
		Cause:   err,
		// Location: utils.FileWithLineNum(),

	}
}

// NewNotFoundError Create a 404 Not Found Error.
//
//	err error The error to wrap.
//	message string The generic message to show.
func NewNotFoundError(err error, message string) *AppError {
	return &AppError{
		Code:    http.StatusNotFound,
		Message: message,
		Cause:   err,
		// Location: utils.FileWithLineNum(),
	}
}

// NewInternalServerError Create a 500 Internal Server Error.
//
//	err error The error to wrap.
//	message string The generic message to show.
func NewInternalServerError(err error, message string) *AppError {
	return &AppError{
		Code:    http.StatusInternalServerError,
		Message: message,
		Cause:   err,
		// Location: utils.FileWithLineNum(),
	}
}

// NewDBError Create a 500 Internal Server Error due to a DB failure.
//
//	err error The error to wrap.
//	message string The generic message to show.
func NewDBError(err error, message string) *AppError {
	return &AppError{
		Code:    http.StatusInternalServerError,
		Message: message,
		Cause:   err,
		// Location: utils.FileWithLineNum(),
	}
}

// NewAuthorizationError Create a 401 Unauthorized.
//
//	err error The error to wrap.
//	message string The generic message to show.
func NewAuthorizationError(err error, message string) *AppError {
	return &AppError{
		Code:    http.StatusUnauthorized,
		Message: message,
		Cause:   err,
		// Location: utils.FileWithLineNum(),
	}
}

// NewForbiddenError Create a 403 Forbidden Error.
//
//	err error The error to wrap.
//	message string The generic message to show.
func NewForbiddenError(err error, message string) *AppError {
	return &AppError{
		Code:    http.StatusForbidden,
		Message: message,
		Cause:   err,
		// Location: utils.FileWithLineNum(),
	}
}
